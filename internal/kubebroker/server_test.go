package kubebroker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/kube"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	apps "k8s.io/api/apps/v1"
	auth "k8s.io/api/authentication/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kfake "k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fixture(t *testing.T) (*Broker, *auth.TokenReviewStatus) {
	t.Helper()
	yes := true
	c := fake.NewClientBuilder().WithScheme(kube.Scheme()).WithObjects(
		&apps.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "one-forwarder", Namespace: "system", UID: "ds"}},
		&core.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", UID: "node-uid"}},
		&core.Pod{ObjectMeta: metav1.ObjectMeta{Name: "forwarder", Namespace: "system", UID: "pod-a", OwnerReferences: []metav1.OwnerReference{{Kind: "DaemonSet", Name: "one-forwarder", UID: "ds", Controller: &yes}}}, Spec: core.PodSpec{NodeName: "node-a", ServiceAccountName: "one-forwarder"}},
	).Build()
	status := &auth.TokenReviewStatus{Authenticated: true, Audiences: []string{"audience"}, User: auth.UserInfo{Username: "system:serviceaccount:system:one-forwarder", Extra: map[string]auth.ExtraValue{"authentication.kubernetes.io/pod-name": {"forwarder"}, "authentication.kubernetes.io/pod-uid": {"pod-a"}}}}
	tokens := kfake.NewSimpleClientset()
	tokens.PrependReactor("create", "tokenreviews", func(ktest.Action) (bool, runtime.Object, error) { return true, &auth.TokenReview{Status: *status}, nil })
	b := &Broker{Kube: c, Tokens: tokens, Namespace: "system", Installation: "one", DaemonSet: "one-forwarder", ServiceAccount: "one-forwarder", Audience: "audience", ManagementURL: "http://control", ControllerID: "controller"}

	return b, status
}
func call(b *Broker, path, body string) *httptest.ResponseRecorder {
	if body == "" {
		body = "{}"
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Connect-Protocol-Version", "1")
	r.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, r)
	return w
}

func TestBrokerRejectsWrongIdentity(t *testing.T) {
	for _, kind := range []string{"account", "audience", "uid", "owner", "node"} {
		t.Run(kind, func(t *testing.T) {
			b, status := fixture(t)
			switch kind {
			case "account":
				status.User.Username = "system:serviceaccount:other:one-forwarder"
			case "audience":
				status.Audiences = []string{"other"}
			case "uid":
				status.User.Extra["authentication.kubernetes.io/pod-uid"] = auth.ExtraValue{"spoof"}
			case "owner":
				b.DaemonSet = "other"
			case "node":
				var p core.Pod
				_ = b.Kube.Get(context.Background(), types.NamespacedName{Namespace: "system", Name: "forwarder"}, &p)
				p.Spec.NodeName = "missing"
				_ = b.Kube.Update(context.Background(), &p)
			}
			if w := call(b, "/funnel.v1.BrokerService/OpenSession", `{}`); w.Code != 403 {
				t.Fatal(kind, w.Code)
			}
		})
	}
}
func TestLeaseExclusionExpiryAndRelease(t *testing.T) {
	b, _ := fixture(t)
	now := time.Now()
	b.Now = func() time.Time { return now }
	c := caller{pod: core.Pod{ObjectMeta: metav1.ObjectMeta{UID: "one"}}, key: "lease"}
	if e := b.lease(context.Background(), c, true); e != nil {
		t.Fatal(e)
	}
	other := c
	other.pod.UID = "two"
	if e := b.lease(context.Background(), other, true); e == nil {
		t.Fatal("concurrent identity acquired")
	}
	now = now.Add(61 * time.Second)
	if e := b.lease(context.Background(), c, false); e == nil {
		t.Fatal("expired lease renewed")
	}
	if e := b.lease(context.Background(), other, true); e != nil {
		t.Fatal(e)
	}
	if e := b.lease(context.Background(), c, false); e == nil {
		t.Fatal("old holder resumed")
	}
}

func TestBrokerReadOnlyHealth(t *testing.T) {
	b, _ := fixture(t)
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestTerminatingPodCanFlushAndReleaseButCannotRenew(t *testing.T) {
	b, _ := fixture(t)
	if w := call(b, "/funnel.v1.BrokerService/OpenSession", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p core.Pod
	key := types.NamespacedName{Namespace: "system", Name: "forwarder"}
	_ = b.Kube.Get(context.Background(), key, &p)
	p.Finalizers = []string{"test-cleanup"}
	if e := b.Kube.Update(context.Background(), &p); e != nil {
		t.Fatal(e)
	}
	if e := b.Kube.Delete(context.Background(), &p); e != nil {
		t.Fatal(e)
	}
	if w := call(b, "/funnel.v1.BrokerService/OpenSession", ""); w.Code != 403 {
		t.Fatal("terminating pod reacquired identity", w.Code)
	}
	if w := call(b, "/funnel.v1.BrokerService/RenewLease", ""); w.Code != 403 {
		t.Fatal("terminating pod renewed lease", w.Code)
	}
	if w := call(b, "/funnel.v1.BrokerService/ReleaseLease", ""); w.Code != 200 {
		t.Fatal("lease not released", w.Code, w.Body.String())
	}
	if w := call(b, "/funnel.v1.BrokerService/WriteState", `{"key":"tsnet.dGVzdA","value":"c3RhbGU="}`); w.Code != 404 {
		t.Fatal("removed identity API remains reachable", w.Code)
	}
}

func TestConfigurationRequiresBoundPodAndOwnedLease(t *testing.T) {
	b, status := fixture(t)
	b.Configuration = func(context.Context) (report.Snapshot, error) {
		return report.NewSnapshot("configuration", "parent", map[string]string{"node_id": "parent"}, time.Now())
	}
	if w := call(b, "/funnel.v1.BrokerService/GetConfig", ""); w.Code != 409 {
		t.Fatal("configuration before lease", w.Code)
	}
	if w := call(b, "/funnel.v1.BrokerService/OpenSession", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call(b, "/funnel.v1.BrokerService/GetConfig", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"nodeId":"parent"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	status.User.Username = "system:serviceaccount:other:one-forwarder"
	if w := call(b, "/funnel.v1.BrokerService/GetConfig", ""); w.Code != 403 {
		t.Fatal("cross-installation configuration access", w.Code)
	}
}

func TestBrokerReadinessDoesNotBypassAuthentication(t *testing.T) {
	b, _ := fixture(t)
	ready := false
	b.Ready = func() bool { return ready }
	handler := b.Handler()
	for _, tc := range []struct {
		method, path string
		want         int
	}{{"GET", "/healthz", 200}, {"GET", "/readyz", 503}, {"POST", "/funnel.v1.BrokerService/OpenSession", 503}} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Connect-Protocol-Version", "1")
		handler.ServeHTTP(w, request)
		if w.Code != tc.want {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	ready = true
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/funnel.v1.BrokerService/OpenSession", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	handler.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("readiness bypassed token authentication", w.Code)
	}
}

func TestBrokerRejectsClientNodeClaim(t *testing.T) {
	b, _ := fixture(t)
	if w := call(b, "/funnel.v1.BrokerService/OpenSession", `{"node_name":"spoof"}`); w.Code != 400 {
		t.Fatalf("unknown identity field accepted: %d", w.Code)
	}
}
