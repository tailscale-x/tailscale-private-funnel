package operator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestEnvtestCRValidationOwnershipAndFinalizer(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("set KUBEBUILDER_ASSETS using pinned setup-envtest; CI requires this gate")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}, ErrorIfCRDPathMissing: true}
	cfg, e := env.Start()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := env.Stop(); e != nil {
			t.Error(e)
		}
	})
	s := testScheme()
	c, e := client.New(cfg, client.Options{Scheme: s})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ns := &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "installation"}}
	if e = c.Create(ctx, ns); e != nil {
		t.Fatal(e)
	}
	bad := testControl()
	bad.Name = "invalid"
	bad.UID = ""
	bad.Spec.Tailnet = ""
	if e = c.Create(ctx, bad); !apierrors.IsInvalid(e) {
		t.Fatalf("invalid CR accepted: %v", e)
	}
	o := testControl()
	o.UID = ""
	o.Finalizers = nil
	if e = c.Create(ctx, o); e != nil {
		t.Fatal(e)
	}
	r := Reconciler{Client: c, Scheme: s, Kind: "FunnelControl", Namespaces: map[string]bool{ns.Name: true}}
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(o)}
	if _, e = r.Reconcile(ctx, request); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Reconcile(ctx, request); e != nil {
		t.Fatal(e)
	}
	var secret core.Secret
	if e = c.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: identityName(o)}, &secret); e != nil {
		t.Fatal(e)
	}
	if secret.Labels[OwnerLabel] != string(o.UID) || len(secret.OwnerReferences) != 1 {
		t.Fatal("identity ownership missing")
	}
	// Cleanup is blocked if runtime reports an active enrolled component.
	secret.Data = map[string][]byte{"active-components": []byte("1"), "initialized": []byte("1")}
	if e = c.Update(ctx, &secret); e != nil {
		t.Fatal(e)
	}
	if e = c.Delete(ctx, o); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Reconcile(ctx, request); e != nil {
		t.Fatal(e)
	}
	var got funnel.FunnelControl
	if e = c.Get(ctx, request.NamespacedName, &got); e != nil {
		t.Fatal(e)
	}
	if len(got.Finalizers) == 0 || len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "DeletionBlocked" {
		t.Fatalf("unsafe deletion: %+v", got.Status)
	}
	// A same-name foreign Secret must remain unchanged.
	foreign := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: ns.Name}, Data: map[string][]byte{"keep": []byte("untouched")}}
	if e = c.Create(ctx, foreign); e != nil {
		t.Fatal(e)
	}
	if e = r.ensure(ctx, &got, &core.Secret{ObjectMeta: foreign.ObjectMeta}); e == nil {
		t.Fatal("foreign resource adopted")
	}

	// A cached owned object must not authorize deleting a same-name replacement.
	old := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "replaced", Namespace: ns.Name, Labels: labels(&got)}, Data: map[string][]byte{"old": []byte("owned")}}
	if e = c.Create(ctx, old); e != nil {
		t.Fatal(e)
	}
	stale := old.DeepCopy()
	if e = c.Delete(ctx, old); e != nil {
		t.Fatal(e)
	}
	replacement := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: old.Name, Namespace: ns.Name}, Data: map[string][]byte{"keep": []byte("foreign")}}
	if e = c.Create(ctx, replacement); e != nil {
		t.Fatal(e)
	}
	if e = r.deleteOwned(ctx, stale); !apierrors.IsConflict(e) {
		t.Fatalf("replacement deletion was not fenced: %v", e)
	}
	var preserved core.Secret
	if e = c.Get(ctx, client.ObjectKeyFromObject(replacement), &preserved); e != nil || preserved.UID != replacement.UID || string(preserved.Data["keep"]) != "foreign" {
		t.Fatal("foreign replacement changed", e)
	}
}
