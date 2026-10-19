package workload

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	ts "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	apps "k8s.io/api/apps/v1"
	auth "k8s.io/api/authentication/v1"
	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kfake "k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIssuerDerivesRoleFromVerifiedWorkload(t *testing.T) {
	for _, bad := range []string{"", "logical-id", "namespace", "audience", "pod-uid", "account", "workload-owner", "component-owner", "missing-issuer"} {
		t.Run(bad, func(t *testing.T) {
			yes := true
			scheme := runtime.NewScheme()
			_ = core.AddToScheme(scheme)
			_ = apps.AddToScheme(scheme)
			_ = funnel.AddToScheme(scheme)
			component := &funnel.FunnelGateway{ObjectMeta: meta.ObjectMeta{Name: "site", Namespace: "installation", UID: "component"}, Spec: funnel.GatewaySpec{ComponentSpec: funnel.ComponentSpec{Installation: "one", KeyIssuerRef: funnel.KeyIssuerReference{Name: "issuer"}}}}
			ds := &apps.DaemonSet{ObjectMeta: meta.ObjectMeta{Name: "site-forwarder", Namespace: "installation", UID: "daemonset", OwnerReferences: []meta.OwnerReference{{APIVersion: funnel.GroupVersion.String(), Kind: "FunnelGateway", Name: "site", UID: "component", Controller: &yes}}}, Spec: apps.DaemonSetSpec{Template: core.PodTemplateSpec{Spec: core.PodSpec{ServiceAccountName: "site-forwarder"}}}}
			pod := &core.Pod{ObjectMeta: meta.ObjectMeta{Name: "forwarder", Namespace: "installation", UID: "pod", OwnerReferences: []meta.OwnerReference{{APIVersion: "apps/v1", Kind: "DaemonSet", Name: ds.Name, UID: ds.UID, Controller: &yes}}}, Spec: core.PodSpec{ServiceAccountName: "site-forwarder", NodeName: "node"}}
			status := auth.TokenReviewStatus{Authenticated: true, Audiences: []string{Audience}, User: auth.UserInfo{Username: "system:serviceaccount:installation:site-forwarder", Extra: map[string]auth.ExtraValue{"authentication.kubernetes.io/pod-name": {"forwarder"}, "authentication.kubernetes.io/pod-uid": {"pod"}}}}
			switch bad {
			case "logical-id":
				component.Spec.ComponentID = "logical-gateway"
			case "namespace":
				status.User.Username = "system:serviceaccount:other:site-forwarder"
			case "audience":
				status.Audiences = []string{"other"}
			case "pod-uid":
				status.User.Extra["authentication.kubernetes.io/pod-uid"] = []string{"foreign"}
			case "account":
				pod.Spec.ServiceAccountName = "other"
			case "workload-owner":
				pod.OwnerReferences[0].UID = "foreign"
			case "component-owner":
				ds.OwnerReferences[0].UID = "foreign"
			case "missing-issuer":
				component.Spec.KeyIssuerRef.Name = "absent"
			}
			kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(component, ds, pod, &core.Node{ObjectMeta: meta.ObjectMeta{Name: "node", UID: "node-uid"}}, &core.Secret{ObjectMeta: meta.ObjectMeta{Name: "issuer", Namespace: "installation"}, Data: map[string][]byte{"client_id": []byte("client"), "client_secret": []byte("secret")}}).Build()
			tokens := kfake.NewSimpleClientset()
			tokens.PrependReactor("create", "tokenreviews", func(a kt.Action) (bool, runtime.Object, error) {
				r := a.(kt.CreateAction).GetObject().(*auth.TokenReview)
				if len(r.Spec.Audiences) != 1 || r.Spec.Audiences[0] != Audience {
					t.Fatal("wrong audience")
				}
				return true, &auth.TokenReview{Status: status}, nil
			})
			minted := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/oauth/token") {
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token", "expires_in": 3600})
					return
				}
				minted++
				var body struct {
					Capabilities struct {
						Devices struct {
							Create struct {
								Tags                               []string
								Ephemeral, Reusable, Preauthorized bool
							}
						}
					}
					ExpirySeconds int
				}
				if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
					t.Fatal(e)
				}
				c := body.Capabilities.Devices.Create
				if len(c.Tags) != 1 || c.Tags[0] != identity.Tag("one", identity.KubernetesGateway) || !c.Ephemeral || c.Reusable || !c.Preauthorized || body.ExpirySeconds != 900 {
					t.Fatalf("unsafe issued key settings: %+v", body)
				}
				_ = json.NewEncoder(w).Encode(map[string]string{"key": "tskey-test"})
			}))
			defer api.Close()
			i := Issuer{Kube: kube, Tokens: tokens, Namespaces: map[string]bool{"installation": true}, TailscaleClient: func(id, secret, role string) ts.Client {
				if id != "client" || secret != "secret" || role != "k8s-gateway" {
					t.Fatal("issuer selection mismatch")
				}
				return ts.Client{ClientID: id, ClientSecret: secret, Tailnet: "-", HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
			}}
			req := connect.NewRequest(&v1.Empty{})
			req.Header().Set("Authorization", "Bearer projected-token")
			out, e := i.IssueKey(t.Context(), req)
			if bad != "" && bad != "logical-id" {
				if e == nil || minted != 0 {
					t.Fatal("unverified workload received a key", e, minted)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			parent := "component"
			if bad == "logical-id" {
				parent = "logical-gateway"
			}
			if out.Msg.ComponentId != ForwarderID(parent, "node-uid") || out.Msg.ParentId != parent || out.Msg.InstanceId != "pod" || out.Msg.Mode != "k8s-forwarder" || minted != 1 {
				t.Fatal(out.Msg)
			}
		})
	}
}
