package kubebroker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	apps "k8s.io/api/apps/v1"
	auth "k8s.io/api/authentication/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

type Enrollment struct{ NodeID, AuthKey string }
type Broker struct {
	Kube                                                                                    client.Client
	Tokens                                                                                  kubernetes.Interface
	Namespace, Installation, DaemonSet, ServiceAccount, Audience, ManagementURL, ControlKey string
	Configuration                                                                           func(context.Context) (report.Envelope, error)
	ControllerID                                                                            string
	Enroll                                                                                  func(context.Context, string, string, string) (Enrollment, error)
	Allowed                                                                                 func(context.Context, string) (bool, error)
	Ready                                                                                   func() bool
	Now                                                                                     func() time.Time
}
type caller struct {
	pod  core.Pod
	node core.Node
	key  string
}

func (b *Broker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}
func (b *Broker) authenticate(r *http.Request) (caller, error) {
	var out caller
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || len(token) > 32768 {
		return out, fmt.Errorf("token required")
	}
	review, e := b.Tokens.AuthenticationV1().TokenReviews().Create(r.Context(), &auth.TokenReview{Spec: auth.TokenReviewSpec{Token: token, Audiences: []string{b.Audience}}}, metav1.CreateOptions{})
	if e != nil || !review.Status.Authenticated || review.Status.User.Username != "system:serviceaccount:"+b.Namespace+":"+b.ServiceAccount {
		return out, fmt.Errorf("invalid service account")
	}
	audience := false
	for _, a := range review.Status.Audiences {
		audience = audience || a == b.Audience
	}
	if !audience {
		return out, fmt.Errorf("invalid audience")
	}
	names := review.Status.User.Extra["authentication.kubernetes.io/pod-name"]
	uids := review.Status.User.Extra["authentication.kubernetes.io/pod-uid"]
	if len(names) != 1 || len(uids) != 1 {
		return out, fmt.Errorf("bound pod token required")
	}
	if e = b.Kube.Get(r.Context(), types.NamespacedName{Namespace: b.Namespace, Name: names[0]}, &out.pod); e != nil {
		return out, e
	}
	if string(out.pod.UID) != uids[0] || (out.pod.DeletionTimestamp != nil && r.URL.Path != "/v1/release" && r.URL.Path != "/v1/state") || out.pod.Spec.ServiceAccountName != b.ServiceAccount || out.pod.Spec.NodeName == "" {
		return out, fmt.Errorf("invalid pod binding")
	}
	var ds apps.DaemonSet
	if e = b.Kube.Get(r.Context(), types.NamespacedName{Namespace: b.Namespace, Name: b.DaemonSet}, &ds); e != nil {
		return out, e
	}
	owned := false
	for _, o := range out.pod.OwnerReferences {
		owned = owned || o.Kind == "DaemonSet" && o.Name == ds.Name && o.UID == ds.UID && o.Controller != nil && *o.Controller
	}
	if !owned {
		return out, fmt.Errorf("pod is not owned by this installation")
	}
	if e = b.Kube.Get(r.Context(), types.NamespacedName{Name: out.pod.Spec.NodeName}, &out.node); e != nil {
		return out, e
	}
	out.key = fmt.Sprintf("%s-node-%x", b.Installation, sha256.Sum256([]byte(out.node.UID)))[:len(b.Installation)+6+24]
	return out, nil
}
func (b *Broker) lease(ctx context.Context, c caller, acquire bool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var l coordination.Lease
		key := types.NamespacedName{Namespace: b.Namespace, Name: c.key}
		e := b.Kube.Get(ctx, key, &l)
		now := metav1.NewMicroTime(b.now())
		holder := string(c.pod.UID)
		duration := int32(60)
		if apierrors.IsNotFound(e) && acquire {
			return b.Kube.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: c.key, Namespace: b.Namespace}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, RenewTime: &now}})
		}
		if e != nil {
			return e
		}
		current := ""
		if l.Spec.HolderIdentity != nil {
			current = *l.Spec.HolderIdentity
		}
		expired := l.Spec.RenewTime == nil || l.Spec.RenewTime.Add(60*time.Second).Before(b.now())
		if current != holder && (!acquire || !expired) {
			return fmt.Errorf("identity lease is held by another pod")
		}
		if !acquire && expired {
			return fmt.Errorf("identity lease expired; rejoin required")
		}
		l.Spec.HolderIdentity = &holder
		l.Spec.RenewTime = &now
		l.Spec.LeaseDurationSeconds = &duration
		return b.Kube.Update(ctx, &l)
	})
}
func (b *Broker) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			if b.Ready != nil && !b.Ready() {
				http.Error(w, "controller joining or stopping", 503)
				return
			}
			w.WriteHeader(200)
			return
		}
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		if b.Ready != nil && !b.Ready() {
			http.Error(w, "controller joining or stopping", 503)
			return
		}
		bounded, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		r = r.WithContext(bounded)
		c, e := b.authenticate(r)
		if e != nil {
			http.Error(w, "forwarder authentication failed", 403)
			return
		}
		ctx := r.Context()
		if b.Allowed != nil {
			var state core.Secret
			e := b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: c.key}, &state)
			if e == nil && len(state.Data["node-id"]) > 0 {
				allowed, e := b.Allowed(ctx, string(state.Data["node-id"]))
				if e != nil {
					http.Error(w, "control authorization unavailable", 503)
					return
				}
				if !allowed {
					state.Data = map[string][]byte{"revoked": []byte("true"), "node-id": state.Data["node-id"], "node-uid": state.Data["node-uid"]}
					_ = b.Kube.Update(ctx, &state)
					http.Error(w, "forwarder revoked", 403)
					return
				}
			} else if e != nil && (!apierrors.IsNotFound(e) || r.URL.Path != "/v1/session") {
				http.Error(w, "state unavailable", 503)
				return
			}
		}
		if r.URL.Path == "/v1/session" {
			b.session(w, r, c)
			return
		}
		if e = b.lease(ctx, c, false); e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		switch r.URL.Path {
		case "/v1/config":
			if b.Configuration == nil {
				http.Error(w, "configuration unavailable", 503)
				return
			}
			config, err := b.Configuration(ctx)
			if err != nil {
				http.Error(w, "configuration unavailable", 503)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(config)
		case "/v1/renew":
			w.WriteHeader(204)
		case "/v1/state":
			var in struct {
				Key   string `json:"key"`
				Value []byte `json:"value"`
			}
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
			dec.DisallowUnknownFields()
			if dec.Decode(&in) != nil || !strings.HasPrefix(in.Key, "tsnet.") || len(in.Key) > 512 {
				http.Error(w, "invalid state update", 400)
				return
			}
			e = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				var s core.Secret
				if e := b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: c.key}, &s); e != nil {
					return e
				}
				s.Data[in.Key] = in.Value
				return b.Kube.Update(ctx, &s)
			})
			if e != nil {
				http.Error(w, "state persistence failed", 503)
				return
			}
			w.WriteHeader(204)
		case "/v1/release":
			var l coordination.Lease
			e = b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: c.key}, &l)
			if e == nil {
				zero := ""
				l.Spec.HolderIdentity = &zero
				l.Spec.RenewTime = nil
				e = b.Kube.Update(ctx, &l)
			}
			if e != nil {
				http.Error(w, "lease release failed", 503)
				return
			}
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})
}
func (b *Broker) session(w http.ResponseWriter, r *http.Request, c caller) {
	ctx := r.Context()
	if e := b.lease(ctx, c, true); e != nil {
		http.Error(w, e.Error(), 409)
		return
	}
	var s core.Secret
	e := b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: c.key}, &s)
	if apierrors.IsNotFound(e) {
		pub, priv, err := report.GenerateKey()
		if err != nil {
			http.Error(w, "identity generation failed", 503)
			return
		}
		identity, _ := json.Marshal(map[string]string{"public": base64.RawStdEncoding.EncodeToString(pub), "private": base64.RawStdEncoding.EncodeToString(priv)})
		s = core.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: b.Namespace, Name: c.key, Labels: map[string]string{"app.kubernetes.io/instance": b.Installation}}, Data: map[string][]byte{"identity": identity, "node-uid": []byte(c.node.UID)}}
		if e = b.Kube.Create(ctx, &s); e != nil {
			http.Error(w, "identity reservation failed", 503)
			return
		}
	} else if e != nil {
		http.Error(w, "state unavailable", 503)
		return
	}
	if string(s.Data["node-uid"]) != string(c.node.UID) || len(s.Data["identity"]) == 0 {
		http.Error(w, "corrupt identity state", 409)
		return
	}
	out := Session{ControllerID: b.ControllerID, NodeUID: string(c.node.UID), NodeName: c.node.Name, NodeID: string(s.Data["node-id"]), ManagementURL: b.ManagementURL, ControlKey: b.ControlKey, Identity: s.Data["identity"], State: map[string][]byte{}}
	for k, v := range s.Data {
		if strings.HasPrefix(k, "tsnet.") {
			out.State[k] = v
		}
	}
	if len(out.State) == 0 {
		var identity struct {
			Public string `json:"public"`
		}
		if json.Unmarshal(out.Identity, &identity) != nil || identity.Public == "" {
			http.Error(w, "corrupt signing identity", 409)
			return
		}
		hostname := fmt.Sprintf("%s-%x", b.Installation, sha256.Sum256([]byte(c.node.UID)))
		if len(hostname) > 63 {
			hostname = hostname[:63]
		}
		enrollment, e := b.Enroll(ctx, string(c.node.UID), hostname, identity.Public)
		if e != nil {
			http.Error(w, e.Error(), 503)
			return
		}
		out.AuthKey = enrollment.AuthKey
		out.NodeID = enrollment.NodeID
		s.Data["node-id"] = []byte(out.NodeID)
		if e = b.Kube.Update(ctx, &s); e != nil {
			http.Error(w, "binding persistence failed", 503)
			return
		}
	} else if out.NodeID == "" {
		http.Error(w, "corrupt enrollment binding", 409)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
