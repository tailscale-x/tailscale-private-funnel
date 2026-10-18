package operator

import (
	"strings"
	"testing"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	apps "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestTransferSuspendsOperatorWithoutBootstrapOrRevocation(t *testing.T) {
	o := testControl()
	o.Spec.Transfer = &funnel.ControlTransferState{ID: "transfer", Phase: "Stopped"}
	s := testScheme()
	kubescheme.AddToScheme(s)
	identity := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "control-identity", Namespace: o.Namespace, UID: "identity-uid", Labels: labels(o)}, Data: map[string][]byte{"private": []byte("preserve")}}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(o).WithObjects(o, identity).Build()
	r := &Reconciler{Client: c, Scheme: s}
	if _, err := r.reconcileTransfer(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	var d apps.Deployment
	if err := c.Get(t.Context(), types.NamespacedName{Namespace: o.Namespace, Name: "control-control"}, &d); err != nil {
		t.Fatal(err)
	}
	if *d.Spec.Replicas != 0 {
		t.Fatal("operator restarted suspended source")
	}
	var jobs batch.JobList
	c.List(t.Context(), &jobs)
	if len(jobs.Items) > 0 {
		t.Fatal("bootstrap/cleanup job created")
	}
	var saved core.Secret
	c.Get(t.Context(), client.ObjectKeyFromObject(identity), &saved)
	if string(saved.Data["private"]) != "preserve" {
		t.Fatal("identity overwritten")
	}
	condition := meta.FindStatusCondition(o.Status.Conditions, "Ready")
	if condition == nil || condition.Reason != "TransferStopped" {
		t.Fatal(condition)
	}
	if _, err := r.cleanupTransferred(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(meta.FindStatusCondition(o.Status.Conditions, "Ready").Reason, "Blocked") {
		t.Fatal("unverified removal allowed")
	}
}
func TestTransferRemovalPreservesReferencedResources(t *testing.T) {
	o := testControl()
	o.Spec.Transfer = &funnel.ControlTransferState{ID: "transfer", Phase: "Transferred", InstallerSHA256: strings.Repeat("a", 64)}
	s := testScheme()
	kubescheme.AddToScheme(s)
	owned := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "control-identity", Namespace: o.Namespace, UID: "identity-uid", Labels: labels(o)}}
	external := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "external-db", Namespace: o.Namespace}, Data: map[string][]byte{"uri": []byte("external")}}
	pvc := &core.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "external-storage", Namespace: o.Namespace}}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(o).WithObjects(o, owned, external, pvc).Build()
	r := &Reconciler{Client: c, Scheme: s}
	if _, err := r.cleanupTransferred(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(external), &core.Secret{}); err != nil {
		t.Fatal("external Secret removed", err)
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(pvc), &core.PersistentVolumeClaim{}); err != nil {
		t.Fatal("external volume removed", err)
	}
	var jobs batch.JobList
	c.List(t.Context(), &jobs)
	if len(jobs.Items) > 0 {
		t.Fatal("revocation job created")
	}
}
