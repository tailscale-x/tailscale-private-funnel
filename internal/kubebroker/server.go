package kubebroker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
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
	"sigs.k8s.io/controller-runtime/pkg/client"
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
func (b *Broker) authenticate(ctx context.Context, authorization, procedure string) (caller, error) {
	var out caller
	token := strings.TrimPrefix(authorization, "Bearer ")
	if token == "" || len(token) > 32768 {
		return out, fmt.Errorf("token required")
	}
	review, e := b.Tokens.AuthenticationV1().TokenReviews().Create(ctx, &auth.TokenReview{Spec: auth.TokenReviewSpec{Token: token, Audiences: []string{b.Audience}}}, metav1.CreateOptions{})
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
	if e = b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: names[0]}, &out.pod); e != nil {
		return out, e
	}
	if string(out.pod.UID) != uids[0] || (out.pod.DeletionTimestamp != nil && procedure != funnelv1connect.BrokerServiceReleaseLeaseProcedure && procedure != funnelv1connect.BrokerServiceWriteStateProcedure) || out.pod.Spec.ServiceAccountName != b.ServiceAccount || out.pod.Spec.NodeName == "" {
		return out, fmt.Errorf("invalid pod binding")
	}
	var ds apps.DaemonSet
	if e = b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: b.DaemonSet}, &ds); e != nil {
		return out, e
	}
	owned := false
	for _, o := range out.pod.OwnerReferences {
		owned = owned || o.Kind == "DaemonSet" && o.Name == ds.Name && o.UID == ds.UID && o.Controller != nil && *o.Controller
	}
	if !owned {
		return out, fmt.Errorf("pod is not owned by this installation")
	}
	if e = b.Kube.Get(ctx, types.NamespacedName{Name: out.pod.Spec.NodeName}, &out.node); e != nil {
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
			return b.Kube.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: c.key, Namespace: b.Namespace, Labels: ownedRuntimeLabels()}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, RenewTime: &now}})
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
func (b *Broker) session(ctx context.Context, c caller) (*v1.BrokerSession, error) {
	if e := b.lease(ctx, c, true); e != nil {
		return nil, connect.NewError(connect.CodeAborted, errors.New(e.Error()))
	}
	var s core.Secret
	e := b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: c.key}, &s)
	if apierrors.IsNotFound(e) {
		pub, priv, err := report.GenerateKey()
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("identity generation failed"))
		}
		identity, _ := json.Marshal(map[string]string{"public": base64.RawStdEncoding.EncodeToString(pub), "private": base64.RawStdEncoding.EncodeToString(priv)})
		s = core.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: b.Namespace, Name: c.key, Labels: map[string]string{"app.kubernetes.io/instance": b.Installation}}, Data: map[string][]byte{"identity": identity, "node-uid": []byte(c.node.UID)}}
		if uid := os.Getenv("FUNNEL_OWNER_UID"); uid != "" {
			s.Labels["tailscale-private-funnel.io/owner-uid"] = uid
		}
		if e = b.Kube.Create(ctx, &s); e != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("identity reservation failed"))
		}
	} else if e != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("state unavailable"))
	}
	if string(s.Data["node-uid"]) != string(c.node.UID) || len(s.Data["identity"]) == 0 {
		return nil, connect.NewError(connect.CodeAborted, errors.New("corrupt identity state"))
	}
	out := &v1.BrokerSession{ControllerId: b.ControllerID, NodeUid: string(c.node.UID), NodeName: c.node.Name, NodeId: string(s.Data["node-id"]), ManagementUrl: b.ManagementURL, ControlKey: b.ControlKey, Identity: s.Data["identity"], State: map[string][]byte{}}
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
			return nil, connect.NewError(connect.CodeAborted, errors.New("corrupt signing identity"))
		}
		hostname := fmt.Sprintf("%s-%x", b.Installation, sha256.Sum256([]byte(c.node.UID)))
		if len(hostname) > 63 {
			hostname = hostname[:63]
		}
		enrollment, e := b.Enroll(ctx, string(c.node.UID), hostname, identity.Public)
		if e != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New(e.Error()))
		}
		out.AuthKey = enrollment.AuthKey
		out.NodeId = enrollment.NodeID
		s.Data["node-id"] = []byte(out.NodeId)
		if e = b.Kube.Update(ctx, &s); e != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("binding persistence failed"))
		}
	} else if out.NodeId == "" {
		return nil, connect.NewError(connect.CodeAborted, errors.New("corrupt enrollment binding"))
	}
	return out, nil
}
