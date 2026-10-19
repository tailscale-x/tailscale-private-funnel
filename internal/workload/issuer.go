package workload

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	ts "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	apps "k8s.io/api/apps/v1"
	auth "k8s.io/api/authentication/v1"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const Audience = "tailscale-private-funnel/operator-bootstrap"

type Issuer struct {
	Kube            client.Client
	Tokens          kubernetes.Interface
	Namespaces      map[string]bool
	TailscaleClient func(string, string, string) ts.Client
}

func (i *Issuer) Handler() http.Handler {
	_, h := funnelv1connect.NewWorkloadBootstrapServiceHandler(i, connect.WithReadMaxBytes(4096))
	return h
}
func ComponentID(uid types.UID) string { return string(uid) }
func ForwarderID(parent string, node types.UID) string {
	s := sha256.Sum256([]byte(parent + ":" + string(node)))
	return fmt.Sprintf("forwarder-%x", s[:16])
}
func owner(refs []metav1.OwnerReference) (metav1.OwnerReference, error) {
	for _, r := range refs {
		if r.Controller != nil && *r.Controller {
			return r, nil
		}
	}
	return metav1.OwnerReference{}, errors.New("controller ownership required")
}
func (i *Issuer) IssueKey(ctx context.Context, r *connect.Request[v1.Empty]) (*connect.Response[v1.WorkloadBootstrap], error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	token := strings.TrimPrefix(r.Header().Get("Authorization"), "Bearer ")
	if token == "" || len(token) > 32768 {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("bound Kubernetes token required"))
	}
	review, e := i.Tokens.AuthenticationV1().TokenReviews().Create(ctx, &auth.TokenReview{Spec: auth.TokenReviewSpec{Token: token, Audiences: []string{Audience}}}, metav1.CreateOptions{})
	if e != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("Kubernetes token validation unavailable"))
	}
	validAudience := false
	for _, a := range review.Status.Audiences {
		validAudience = validAudience || a == Audience
	}
	parts := strings.Split(review.Status.User.Username, ":")
	if !review.Status.Authenticated || !validAudience || len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" || !i.Namespaces[parts[2]] {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("bootstrap namespace or audience denied"))
	}
	names, uids := review.Status.User.Extra["authentication.kubernetes.io/pod-name"], review.Status.User.Extra["authentication.kubernetes.io/pod-uid"]
	if len(names) != 1 || len(uids) != 1 {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("Pod-bound token required"))
	}
	var pod core.Pod
	if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: parts[2], Name: names[0]}, &pod); e != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("Pod unavailable"))
	}
	if pod.DeletionTimestamp != nil || string(pod.UID) != uids[0] || pod.Spec.ServiceAccountName != parts[3] || pod.Spec.NodeName == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("Pod binding mismatch"))
	}
	ref, e := owner(pod.OwnerReferences)
	if e != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, e)
	}
	forwarder := false
	cleanup := false
	switch ref.Kind {
	case "ReplicaSet":
		var rs apps.ReplicaSet
		if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, &rs); e != nil || rs.UID != ref.UID {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("ReplicaSet ownership mismatch"))
		}
		ref, e = owner(rs.OwnerReferences)
		if e != nil {
			return nil, connect.NewError(connect.CodePermissionDenied, e)
		}
		fallthrough
	case "Deployment":
		var d apps.Deployment
		if ref.Kind != "Deployment" {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("Deployment required"))
		}
		if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, &d); e != nil || d.UID != ref.UID || d.Spec.Template.Spec.ServiceAccountName != pod.Spec.ServiceAccountName {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("Deployment binding mismatch"))
		}
		ref, e = owner(d.OwnerReferences)
	case "Job":
		var job batch.Job
		if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, &job); e != nil || job.UID != ref.UID || job.Spec.Template.Spec.ServiceAccountName != pod.Spec.ServiceAccountName || job.Labels["tailscale-private-funnel.io/cleanup"] != "true" {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("cleanup Job ownership mismatch"))
		}
		ref, e = owner(job.OwnerReferences)
		cleanup = true
	case "DaemonSet":
		var d apps.DaemonSet
		if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, &d); e != nil || d.UID != ref.UID || d.Spec.Template.Spec.ServiceAccountName != pod.Spec.ServiceAccountName {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("DaemonSet binding mismatch"))
		}
		ref, e = owner(d.OwnerReferences)
		forwarder = true
	default:
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("unsupported Pod controller"))
	}
	if e != nil || ref.APIVersion != funnel.GroupVersion.String() {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("Funnel component ownership required"))
	}
	var spec funnel.ComponentSpec
	var uid types.UID
	var role identity.Role
	switch ref.Kind {
	case "FunnelControl":
		var c funnel.FunnelControl
		if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, &c); e == nil {
			if c.DeletionTimestamp != nil && !cleanup {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("component deleting"))
			}
			spec = c.Spec.ComponentSpec
			uid = c.UID
		}
		role = identity.Control
	case "FunnelLoadBalancer":
		var c funnel.FunnelLoadBalancer
		if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, &c); e == nil {
			if c.DeletionTimestamp != nil && !cleanup {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("component deleting"))
			}
			spec = c.Spec.ComponentSpec
			uid = c.UID
		}
		role = identity.LoadBalancer
	case "FunnelGateway":
		var c funnel.FunnelGateway
		if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, &c); e == nil {
			if c.DeletionTimestamp != nil && !cleanup {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("component deleting"))
			}
			spec = c.Spec.ComponentSpec
			uid = c.UID
		}
		role = identity.KubernetesGateway
	default:
		e = errors.New("unsupported component")
	}
	if e != nil || uid != ref.UID || forwarder && role != identity.KubernetesGateway {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("component ownership mismatch"))
	}
	if spec.Installation == "" {
		spec.Installation = "main"
	}
	if e = identity.ValidateInstallation(spec.Installation); e != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, e)
	}
	if spec.KeyIssuerRef.Name == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("installation keyIssuerRef required"))
	}
	var secret core.Secret
	if e = i.Kube.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: spec.KeyIssuerRef.Name}, &secret); e != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("key issuer unavailable"))
	}
	idKey, secretKey := spec.KeyIssuerRef.ClientIDKey, spec.KeyIssuerRef.ClientSecretKey
	if idKey == "" {
		idKey = "client_id"
	}
	if secretKey == "" {
		secretKey = "client_secret"
	}
	id, credential := string(secret.Data[idKey]), string(secret.Data[secretKey])
	if id == "" || credential == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("key issuer credentials missing"))
	}
	logicalID := spec.ComponentID
	if logicalID == "" {
		logicalID = ComponentID(uid)
	}
	out := &v1.WorkloadBootstrap{ComponentId: logicalID, InstanceId: string(pod.UID), Installation: spec.Installation, Mode: string(role), ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	if forwarder {
		var node core.Node
		if e = i.Kube.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, &node); e != nil {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("assigned node unavailable"))
		}
		out.ParentId = out.ComponentId
		out.ComponentId = ForwarderID(logicalID, node.UID)
		out.Mode = "k8s-forwarder"
		out.NodeUid = string(node.UID)
		out.NodeName = node.Name
	}
	tsClient := ts.Client{ClientID: id, ClientSecret: credential, Tailnet: "-"}
	if i.TailscaleClient != nil {
		tsClient = i.TailscaleClient(id, credential, string(role))
	}
	out.AuthKey, e = tsClient.CreateWorkloadAuthKey(ctx, out.Mode, pod.Name, []string{identity.Tag(spec.Installation, role)}, true)
	if e != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("installation issuer could not mint the verified workload role"))
	}
	return connect.NewResponse(out), nil
}
