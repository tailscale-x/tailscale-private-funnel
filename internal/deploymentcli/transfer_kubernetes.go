package deploymentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	"github.com/tailscale-x/tailscale-private-funnel/internal/operator"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type kubernetesTransferSource struct {
	client client.Client
	flags  Flags
	object *funnel.FunnelControl
	helper *core.Pod
}

func newKubernetesTransferSource(ctx context.Context, name string, f Flags) (*kubernetesTransferSource, error) {
	k, err := kubeClient(f)
	if err != nil {
		return nil, err
	}
	var op apps.Deployment
	if err = k.Get(ctx, types.NamespacedName{Namespace: OperatorNamespace, Name: "tailscale-private-funnel-k8s-operator"}, &op); err != nil {
		return nil, errors.New("source operator is unavailable")
	}
	if op.Status.ReadyReplicas != 1 || !operatorManages(op, f.Namespace) {
		return nil, errors.New("source operator is not ready or does not manage this namespace")
	}
	supported := false
	for _, c := range op.Spec.Template.Spec.Containers {
		if c.Image == "ghcr.io/tailscale-x/tailscale-private-funnel:"+f.Version {
			supported = true
		}
	}
	if !supported {
		return nil, errors.New("upgrade the source operator to the selected transfer version before transfer")
	}
	var crd unstructured.Unstructured
	crd.SetAPIVersion("apiextensions.k8s.io/v1")
	crd.SetKind("CustomResourceDefinition")
	if err = k.Get(ctx, types.NamespacedName{Name: "funnelcontrols.tailscale-private-funnel.io"}, &crd); err != nil {
		return nil, errors.New("cannot verify source transfer CRD; check CRD read permissions")
	}
	versions, _, _ := unstructured.NestedSlice(crd.Object, "spec", "versions")
	capable := false
	for _, v := range versions {
		item := v.(map[string]any)
		if item["name"] == "v1alpha1" {
			_, capable, _ = unstructured.NestedMap(item, "schema", "openAPIV3Schema", "properties", "spec", "properties", "transfer")
		}
	}
	if !capable {
		return nil, errors.New("upgrade source CRDs before transfer; suspension must not be pruned")
	}
	o := &funnel.FunnelControl{}
	if err = k.Get(ctx, types.NamespacedName{Namespace: f.Namespace, Name: name}, o); err != nil {
		return nil, err
	}
	if o.Spec.Transfer != nil || o.DeletionTimestamp != nil {
		return nil, errors.New("source is already fenced, transferred, or being deleted")
	}
	if !o.Status.Initialized {
		return nil, errors.New("source control is not initialized")
	}
	for _, list := range []client.ObjectList{&funnel.FunnelGatewayList{}, &funnel.FunnelLoadBalancerList{}} {
		if err = k.List(ctx, list, client.InNamespace(o.Namespace)); err != nil {
			return nil, err
		}
	}
	return &kubernetesTransferSource{client: k, flags: f, object: o}, nil
}
func (s *kubernetesTransferSource) Close() {
	if s.helper != nil {
		_ = stopTransferHelper(context.Background(), s.client, s.helper)
	}
}
func (s *kubernetesTransferSource) Settings() (string, string) {
	return s.object.Spec.Hostname, s.object.Spec.Tailnet
}
func (s *kubernetesTransferSource) Paths() []string { return nil }
func (s *kubernetesTransferSource) Fence(ctx context.Context) error {
	id, err := store.NewResourceID()
	if err != nil {
		return err
	}
	s.object.Spec.Transfer = &funnel.ControlTransferState{ID: id, Phase: "Stopped"}
	if err = s.client.Update(ctx, s.object); err != nil {
		return err
	}
	return waitTransferStopped(ctx, s.client, s.object)
}
func (s *kubernetesTransferSource) Export(ctx context.Context) (*app.ControlTransfer, error) {
	var err error
	s.helper, err = newTransferHelper(ctx, s.client, s.object, "ghcr.io/tailscale-x/tailscale-private-funnel:"+s.flags.Version)
	if err != nil {
		return nil, err
	}
	raw, err := transferExec(ctx, s.flags, s.helper, "transfer-export", nil, nil)
	if err != nil {
		_ = stopTransferHelper(context.WithoutCancel(ctx), s.client, s.helper)
		s.helper = nil
		return nil, err
	}
	var b app.ControlTransfer
	if json.Unmarshal(raw, &b) != nil {
		return nil, errors.New("invalid stopped control export")
	}
	return &b, b.Validate()
}
func (s *kubernetesTransferSource) Remove(ctx context.Context, id, digest string) error {
	if s.helper == nil {
		return errors.New("transfer helper missing")
	}
	if err := detachControlReferences(ctx, s.client, s.object); err != nil {
		return err
	}
	if _, err := transferExec(ctx, s.flags, s.helper, "transfer-mark", []string{"--transfer-id", id}, nil); err != nil {
		return err
	}
	if err := stopTransferHelper(ctx, s.client, s.helper); err != nil {
		return err
	}
	s.helper = nil
	if err := s.client.Get(ctx, client.ObjectKeyFromObject(s.object), s.object); err != nil {
		return err
	}
	s.object.Spec.Transfer.Phase = "Transferred"
	s.object.Spec.Transfer.InstallerSHA256 = digest
	if err := s.client.Update(ctx, s.object); err != nil {
		return err
	}
	if err := s.client.Delete(ctx, s.object); err != nil {
		return err
	}
	return waitCR(ctx, s.client, s.object, true)
}
func waitTransferStopped(ctx context.Context, k client.Client, o *funnel.FunnelControl) error {
	return transferPoll(ctx, func() (bool, error) {
		if err := k.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
			return false, err
		}
		condition := meta.FindStatusCondition(o.Status.Conditions, "Ready")
		return condition != nil && condition.Reason == "TransferStopped" && o.Status.ObservedGeneration >= o.Generation, nil
	})
}
func transferPoll(ctx context.Context, f func() (bool, error)) error {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		done, err := f()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
func newTransferHelper(ctx context.Context, k client.Client, o *funnel.FunnelControl, image string) (*core.Pod, error) {
	// Helpers never own a Tailscale identity or run the serving command.
	var deployment apps.Deployment
	if err := k.Get(ctx, types.NamespacedName{Namespace: o.Namespace, Name: o.Name + "-control"}, &deployment); err != nil {
		return nil, err
	}
	if deployment.Spec.Replicas == nil || *deployment.Spec.Replicas != 0 {
		return nil, errors.New("control deployment is not stopped")
	}
	if deployment.Labels[operator.OwnerLabel] != string(o.UID) {
		return nil, errors.New("foreign control deployment ownership")
	}
	spec := deployment.Spec.Template.Spec.DeepCopy()
	spec.InitContainers = nil
	spec.RestartPolicy = core.RestartPolicyNever
	if len(spec.Containers) != 1 {
		return nil, errors.New("unexpected control container inventory")
	}
	c := &spec.Containers[0]
	if image != "" {
		c.Image = image
	}
	if len(c.Args) < 4 || c.Args[0] != "k8s" || c.Args[1] != "runtime" || c.Args[2] != "control" {
		return nil, errors.New("unsupported control runtime")
	}
	c.Args[3] = "transfer-hold"
	c.ReadinessProbe = nil
	c.LivenessProbe = nil
	c.StartupProbe = nil
	c.Ports = nil
	c.Lifecycle = nil
	yes := true
	pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: o.Name + "-transfer-", Namespace: o.Namespace, Labels: map[string]string{operator.OwnerLabel: string(o.UID), "tailscale-private-funnel.io/transfer-helper": "true"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: funnel.GroupVersion.String(), Kind: "FunnelControl", Name: o.Name, UID: o.UID, Controller: &yes}}}, Spec: *spec}
	// Preserve release/component labels for the installation's existing policies.
	for key, value := range deployment.Spec.Template.Labels {
		pod.Labels[key] = value
	}
	if err := k.Create(ctx, pod); err != nil {
		return nil, err
	}
	err := transferPoll(ctx, func() (bool, error) {
		if err := k.Get(ctx, client.ObjectKeyFromObject(pod), pod); err != nil {
			return false, err
		}
		if pod.Status.Phase == core.PodFailed {
			return false, errors.New("transfer helper failed; inspect Pod conditions")
		}
		return pod.Status.Phase == core.PodRunning, nil
	})
	if err != nil {
		_ = stopTransferHelper(context.WithoutCancel(ctx), k, pod)
		return nil, err
	}
	return pod, nil
}
func stopTransferHelper(ctx context.Context, k client.Client, p *core.Pod) error {
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := k.Delete(bounded, p); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return transferPoll(bounded, func() (bool, error) {
		err := k.Get(bounded, client.ObjectKeyFromObject(p), &core.Pod{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
}
func transferExec(ctx context.Context, f Flags, pod *core.Pod, action string, extra []string, input []byte) ([]byte, error) {
	args := []string{"--context", f.Context, "--namespace", f.Namespace}
	if f.Kubeconfig != "" {
		args = append(args, "--kubeconfig", f.Kubeconfig)
	}
	args = append(args, "exec", "-i", pod.Name, "-c", pod.Spec.Containers[0].Name, "--", "/usr/local/bin/tailscale-private-funnel")
	runtime := append([]string(nil), pod.Spec.Containers[0].Args...)
	runtime[3] = action
	args = append(args, runtime...)
	args = append(args, extra...)
	command := exec.CommandContext(ctx, "kubectl", args...)
	command.Stdin = bytes.NewReader(input)
	var out bytes.Buffer
	command.Stdout = &out
	// Do not expose raw helper errors: database driver messages may contain data.
	var diagnostic bytes.Buffer
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("transfer helper %s failed; source/destination remains fenced (%w)", action, err)
	}
	return out.Bytes(), nil
}
func installKubernetesTransfer(ctx context.Context, p *transferPackage) error {
	d := p.Deployment.Deployment
	f := Flags{Context: d.Context, Namespace: d.Namespace, Version: p.Deployment.Version, Hostname: d.Hostname, Tailnet: d.Tailnet, Installation: d.Installation, DisplayName: d.DisplayName, StorageClass: d.StorageClass, StorageSize: d.StorageSize, NodeSelector: d.NodeSelector}
	if d.Database != nil {
		b := d.Database
		f.Database = b.Type
		f.DatabaseSecret = b.ConnectionSecret
		f.DatabaseSecretKey = b.ConnectionSecretKey
		f.DatabaseCASecret = b.CaSecret
		f.DatabaseCASecretKey = b.CaSecretKey
		f.DatabaseTLSMode = b.TlsMode
	}
	k, err := kubeClient(f)
	if err != nil {
		return err
	}
	var op apps.Deployment
	if err = k.Get(ctx, types.NamespacedName{Namespace: OperatorNamespace, Name: "tailscale-private-funnel-k8s-operator"}, &op); err != nil {
		return errors.New("operator unavailable; install the matching operator first")
	}
	if op.Status.ReadyReplicas != 1 || !operatorManages(op, f.Namespace) {
		return errors.New("operator not ready or destination namespace is not managed")
	}
	o := newCR("controls", d.ResourceName, f).(*funnel.FunnelControl)
	o.Spec.BootstrapSecretRef = nil
	o.Spec.Transfer = &funnel.ControlTransferState{ID: p.Control.ID, Phase: "Stopped"}
	if err = applyBundlePlacement(o, d); err != nil {
		return err
	}
	if err = k.Get(ctx, client.ObjectKeyFromObject(o), &funnel.FunnelControl{}); err == nil {
		return errors.New("destination control already exists")
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	// Validate optional rotation before any Kubernetes mutation. OAuth values only
	// enter the encrypted logical data, never CRs or Kubernetes Events.
	control := *p.Control

	bounded, cancel := context.WithTimeout(ctx, time.Duration(d.TimeoutSeconds)*time.Second)
	defer cancel()
	if err = k.Create(bounded, o); err != nil {
		return err
	}
	if err = waitTransferStopped(bounded, k, o); err != nil {
		return err
	}
	helper, err := newTransferHelper(bounded, k, o, p.Deployment.Image)
	if err != nil {
		return err
	}
	defer func() { _ = stopTransferHelper(context.Background(), k, helper) }()
	payload, err := json.Marshal(&control)
	if err != nil {
		return err
	}
	if _, err = transferExec(bounded, f, helper, "transfer-restore", nil, payload); err != nil {
		return err
	}
	if err = stopTransferHelper(bounded, k, helper); err != nil {
		return err
	}
	if err = k.Get(bounded, client.ObjectKeyFromObject(o), o); err != nil {
		return err
	}
	o.Spec.Transfer = nil
	if err = k.Update(bounded, o); err != nil {
		return err
	}
	if d.Wait {
		return waitCR(bounded, k, o, false)
	}
	return nil
}

// Keep enrolled components independent of the source CR's lifecycle. Their
// persistent control URL/verification key remains the same after transfer.
func detachControlReferences(ctx context.Context, k client.Client, source *funnel.FunnelControl) error {
	for _, list := range []client.ObjectList{&funnel.FunnelGatewayList{}, &funnel.FunnelLoadBalancerList{}} {
		if err := k.List(ctx, list, client.InNamespace(source.Namespace)); err != nil {
			return err
		}
		objects, err := meta.ExtractList(list)
		if err != nil {
			return err
		}
		for _, item := range objects {
			object := item.(client.Object)
			spec := crSpec(object)
			if spec.ControlRef == nil || spec.ControlRef.Name != source.Name {
				continue
			}
			if source.Status.SetupURL == "" {
				return errors.New("source control URL missing; cannot preserve referenced components")
			}
			spec.ControlRef = nil
			spec.ManagementURL = source.Status.SetupURL
			if err = k.Update(ctx, object); err != nil {
				return errors.New("could not preserve a component control reference; source remains fenced")
			}
		}
	}
	return nil
}
