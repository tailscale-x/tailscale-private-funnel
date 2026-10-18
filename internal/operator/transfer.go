package operator

import (
	"context"
	"encoding/hex"
	"errors"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	apps "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *Reconciler) reconcileTransfer(ctx context.Context, o *funnel.FunnelControl) (ctrl.Result, error) {
	if o.Spec.Transfer.ID == "" {
		return r.condition(ctx, o, false, "TransferInvalid", "Transfer ID is required")
	}
	desired, err := Render(o)
	if err != nil {
		return ctrl.Result{}, err
	}
	identity := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: identityName(o), Namespace: o.Namespace, Labels: labels(o)}}
	if o.Spec.IdentitySecretRef == nil {
		if err = r.ensure(ctx, o, identity); err != nil {
			return ctrl.Result{}, err
		}
	}
	for _, resource := range desired {
		if resource.GetObjectKind().GroupVersionKind().Kind == "Job" {
			continue
		}
		if resource.GetObjectKind().GroupVersionKind().Kind == "Deployment" {
			u := resource.(*unstructured.Unstructured)
			if err = unstructured.SetNestedField(u.Object, int64(0), "spec", "replicas"); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err = r.ensure(ctx, o, resource); err != nil {
			return ctrl.Result{}, err
		}
	}
	var pods core.PodList
	if err = r.List(ctx, &pods, client.InNamespace(o.Namespace), client.MatchingLabels{OwnerLabel: string(o.UID)}); err != nil {
		return ctrl.Result{}, err
	}
	for _, p := range pods.Items {
		if p.Labels["tailscale-private-funnel.io/transfer-helper"] == "true" {
			continue
		}
		if p.Status.Phase != core.PodSucceeded && p.Status.Phase != core.PodFailed {
			return r.condition(ctx, o, false, "TransferStopping", "Waiting for the original control Pod to terminate")
		}
	}
	return r.condition(ctx, o, false, "TransferStopped", "Control is fenced for transfer; startup and ordinary deletion are disabled")
}
func (r *Reconciler) cleanupTransferred(ctx context.Context, o *funnel.FunnelControl) (ctrl.Result, error) {
	state := o.Spec.Transfer
	digest, e := hex.DecodeString(state.InstallerSHA256)
	if state.Phase != "Transferred" || state.ID == "" || e != nil || len(digest) != 32 {
		return r.condition(ctx, o, false, "TransferBlocked", "A verified installer receipt is required before transfer removal")
	}
	var pods core.PodList
	if e = r.List(ctx, &pods, client.InNamespace(o.Namespace), client.MatchingLabels{OwnerLabel: string(o.UID)}); e != nil {
		return ctrl.Result{}, e
	}
	for _, p := range pods.Items {
		if p.Status.Phase != core.PodFailed && p.Status.Phase != core.PodSucceeded {
			return r.condition(ctx, o, false, "TransferBlocked", "Transfer helper or original control is still running")
		}
	}
	desired, e := Render(o)
	if e != nil {
		return ctrl.Result{}, e
	}
	for _, v := range desired {
		var u unstructured.Unstructured
		u.SetGroupVersionKind(v.GetObjectKind().GroupVersionKind())
		if e = r.Get(ctx, client.ObjectKeyFromObject(v), &u); e == nil {
			if u.GetLabels()[OwnerLabel] == string(o.UID) {
				if e = r.deleteOwned(ctx, &u, client.PropagationPolicy(metav1.DeletePropagationForeground)); e != nil && !apierrors.IsNotFound(e) {
					return ctrl.Result{}, e
				}
			}
		} else if !apierrors.IsNotFound(e) {
			return ctrl.Result{}, e
		}
	}
	for _, list := range []client.ObjectList{&core.SecretList{}, &core.PersistentVolumeClaimList{}, &batch.JobList{}, &apps.DeploymentList{}} {
		if e = r.List(ctx, list, client.InNamespace(o.Namespace), client.MatchingLabels{OwnerLabel: string(o.UID)}); e != nil {
			return ctrl.Result{}, e
		}
		items, e := meta.ExtractList(list)
		if e != nil {
			return ctrl.Result{}, e
		}
		for _, item := range items {
			obj, ok := item.(client.Object)
			if !ok {
				return ctrl.Result{}, errors.New("invalid owned transfer resource")
			}
			if e = r.deleteOwned(ctx, obj, client.PropagationPolicy(metav1.DeletePropagationForeground)); e != nil && !apierrors.IsNotFound(e) {
				return ctrl.Result{}, e
			}
		}
	}
	controllerutil.RemoveFinalizer(o, Finalizer)
	return ctrl.Result{}, r.Update(ctx, o)
}
