package deploymentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/native"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const OperatorNamespace = "tailscale-private-funnel-k8s-operator-system"

func kubeClient(f Flags) (client.Client, error) {
	if strings.TrimSpace(f.Context) == "" {
		return nil, errors.New("--context is required; current context is never implicit")
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if f.Kubeconfig != "" {
		rules.ExplicitPath = f.Kubeconfig
	}
	cfg, e := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: f.Context}).ClientConfig()
	if e != nil {
		return nil, e
	}
	cfg.Timeout = 15 * time.Second
	scheme := runtime.NewScheme()
	_ = core.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)
	_ = funnel.AddToScheme(scheme)
	return client.New(cfg, client.Options{Scheme: scheme})
}
func kubeContextFlags(c *cobra.Command, f *Flags) {
	c.Flags().StringVar(&f.Context, "context", "", "explicit Kubernetes context (required)")
	c.Flags().StringVar(&f.Kubeconfig, "kubeconfig", "", "Kubernetes configuration file")
}
func kubeFlags(c *cobra.Command, f *Flags) {
	kubeContextFlags(c, f)
	c.Flags().StringVar(&f.Namespace, "namespace", "tailscale-private-funnel-main-system", "installation namespace")
}
func newCR(kind, name string, f Flags) client.Object {
	mode := "control"
	if kind == "gateways" {
		mode = "k8s-gateway"
	}
	if kind == "loadbalancers" {
		mode = "loadbalancer"
	}
	spec := funnel.ComponentSpec{Image: provision.ImageRepository + ":" + f.Version, Installation: f.Installation, Hostname: f.Hostname, ManagementURL: f.ControlURL, Placement: funnel.Placement{NodeSelector: f.NodeSelector}}
	if kind == "controls" || kind == "loadbalancers" {
		spec.Storage.Size = f.StorageSize
		if f.StorageClass != "" {
			spec.Storage.StorageClassName = &f.StorageClass
		}
	}
	if spec.Installation == "" {
		spec.Installation = "main"
	}
	if spec.Hostname == "" {
		spec.Hostname = name
	}
	spec.BootstrapSecretRef = &core.LocalObjectReference{Name: name + "-bootstrap"}
	meta := metav1.ObjectMeta{Name: name, Namespace: f.Namespace, Annotations: map[string]string{"tailscale-private-funnel.io/display-name": f.DisplayName}}
	switch mode {
	case "control":
		return &funnel.FunnelControl{TypeMeta: metav1.TypeMeta{APIVersion: funnel.GroupVersion.String(), Kind: "FunnelControl"}, ObjectMeta: meta, Spec: funnel.ControlSpec{ComponentSpec: spec, Tailnet: f.Tailnet}}
	case "loadbalancer":
		return &funnel.FunnelLoadBalancer{TypeMeta: metav1.TypeMeta{APIVersion: funnel.GroupVersion.String(), Kind: "FunnelLoadBalancer"}, ObjectMeta: meta, Spec: funnel.LoadBalancerSpec{ComponentSpec: spec}}
	default:
		spec.Placement = funnel.Placement{}
		gc := f.GatewayClass
		if gc == "" {
			gc = "tailscale-private-funnel-" + name
		}
		ic := f.IngressClass
		if ic == "" {
			ic = gc
		}
		return &funnel.FunnelGateway{TypeMeta: metav1.TypeMeta{APIVersion: funnel.GroupVersion.String(), Kind: "FunnelGateway"}, ObjectMeta: meta, Spec: funnel.GatewaySpec{ComponentSpec: spec, GatewayClass: gc, IngressClass: ic, WatchNamespaces: f.WatchNamespaces, ForwarderPlacement: funnel.Placement{NodeSelector: f.NodeSelector}}}
	}
}
func objectList(kind string) client.ObjectList {
	switch kind {
	case "controls":
		return &funnel.FunnelControlList{}
	case "gateways":
		return &funnel.FunnelGatewayList{}
	default:
		return &funnel.FunnelLoadBalancerList{}
	}
}
func crStatus(o client.Object) *funnel.ComponentStatus {
	switch v := o.(type) {
	case *funnel.FunnelControl:
		return &v.Status
	case *funnel.FunnelGateway:
		return &v.Status
	default:
		return &o.(*funnel.FunnelLoadBalancer).Status
	}
}
func crSpec(o client.Object) *funnel.ComponentSpec {
	switch v := o.(type) {
	case *funnel.FunnelControl:
		return &v.Spec.ComponentSpec
	case *funnel.FunnelGateway:
		return &v.Spec.ComponentSpec
	default:
		return &o.(*funnel.FunnelLoadBalancer).Spec.ComponentSpec
	}
}
func waitCR(ctx context.Context, c client.Client, o client.Object, deleted bool) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		e := c.Get(ctx, client.ObjectKeyFromObject(o), o)
		if deleted && apierrors.IsNotFound(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if !deleted && crStatus(o).ObservedGeneration >= o.GetGeneration() && meta.IsStatusConditionTrue(crStatus(o).Conditions, "Ready") {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s/%s: %w; inspect get conditions", o.GetNamespace(), o.GetName(), ctx.Err())
		case <-tick.C:
		}
	}
}
func privateFile(path string) ([]byte, error) {
	s, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !s.Mode().IsRegular() || s.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential input must be a private regular file (chmod 600)")
	}
	return os.ReadFile(path)
}
func Kubernetes(version string) *cobra.Command {
	root := &cobra.Command{Use: "k8s", Short: "Manage operator-backed deployments in an explicit Kubernetes context"}
	root.AddCommand(operatorCommands(version))
	for _, kind := range []string{"controls", "gateways", "loadbalancers"} {
		parent := &cobra.Command{Use: kind}
		root.AddCommand(parent)
		for _, action := range []string{"create", "list", "get", "update", "delete"} {
			kind, action := kind, action
			f := Flags{}
			use := action + " NAME"
			arg := cobra.ExactArgs(1)
			if action == "list" {
				use = "list"
				arg = cobra.NoArgs
			}
			cmd := &cobra.Command{Use: use, Short: action + " a Kubernetes " + kind + " resource", Args: arg}
			common(cmd, &f, version)
			kubeFlags(cmd, &f)
			if action == "create" {
				creation(cmd, &f, kind, true)
			}
			if action == "update" {
				cmd.Flags().StringVar(&f.ConfigFile, "file", "", "partial component spec JSON; no credentials")
			}
			if action == "delete" {
				cmd.Flags().BoolVar(&f.Yes, "yes", false, "confirm deletion of owned identities, storage and exposures")
			}
			cmd.RunE = func(cmd *cobra.Command, args []string) error {
				if f.Context == "" {
					return errors.New("--context is required")
				}
				if action == "delete" && !f.Yes && !f.DryRun {
					return errors.New("delete requires --yes")
				}
				name := ""
				if len(args) > 0 {
					name = args[0]
					if e := nativeName(name); e != nil {
						return e
					}
				}
				object := newCR(kind, name, f)
				if action == "create" {
					if f.PlacementFile != "" {
						p, e := readPlacement(f.PlacementFile)
						if e != nil {
							return e
						}
						crSpec(object).Placement = *p
					}
					if f.ForwarderPlacementFile != "" {
						p, e := readPlacement(f.ForwarderPlacementFile)
						if e != nil {
							return e
						}
						object.(*funnel.FunnelGateway).Spec.ForwarderPlacement = *p
					}
				}
				if f.DryRun {
					return output(cmd, map[string]any{"dry_run": true, "action": action, "context": f.Context, "resource": object}, f.Output)
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), f.Timeout)
				defer cancel()
				k, e := kubeClient(f)
				if e != nil {
					return e
				}
				if action == "list" {
					list := objectList(kind)
					if e = k.List(ctx, list, client.InNamespace(f.Namespace)); e != nil {
						return e
					}
					return output(cmd, list, f.Output)
				}
				if action != "create" {
					if e = k.Get(ctx, client.ObjectKeyFromObject(object), object); e != nil {
						return e
					}
				}
				switch action {
				case "create":
					var op apps.Deployment
					if e = k.Get(ctx, types.NamespacedName{Namespace: OperatorNamespace, Name: "tailscale-private-funnel-k8s-operator"}, &op); e != nil || op.Status.ReadyReplicas != 1 {
						return errors.New("Kubernetes operator is not ready; run k8s operator create/get first")
					}
					if !operatorManages(op, f.Namespace) {
						return errors.New("namespace is not enabled in this operator installation")
					}
					existing := object.DeepCopyObject().(client.Object)
					if e = k.Get(ctx, client.ObjectKeyFromObject(existing), existing); e == nil {
						return errors.New("resource already exists; use update")
					} else if !apierrors.IsNotFound(e) {
						return e
					}
					secret := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-bootstrap", Namespace: f.Namespace}, Data: map[string][]byte{}}
					if kind == "controls" {
						if f.OAuthID == "" || f.OAuthSecret == "" {
							return errors.New("control creation requires protected OAuth credential files")
						}
						id, e := privateFile(f.OAuthID)
						if e != nil {
							return e
						}
						key, e := privateFile(f.OAuthSecret)
						if e != nil {
							return e
						}
						secret.Data["client-id"] = id
						secret.Data["client-secret"] = key
						secret.Data["administrators"], _ = json.Marshal(f.Administrators)
					} else {
						in, e := request(kind, name, f, "kubernetes")
						if e != nil {
							return e
						}
						rpc, e := apiClient(f.ControlURL)
						if e != nil {
							return e
						}
						authorized, e := rpc.Authorize(ctx, connect.NewRequest(in))
						if e != nil {
							return e
						}
						p := authorized.Msg
						c := p.Credentials
						secret.Data = map[string][]byte{"enrollment-id": []byte(c.EnrollmentId), "code": []byte(c.EnrollmentCode), "auth-key": []byte(c.AuthKey), "control-key": []byte(c.ControlKey), "management-url": []byte(p.ManagementUrl), "hostname": []byte(p.Deployment.Hostname), "cert-email": []byte(p.Deployment.CertificateEmail), "loadbalancer-hostname": []byte(p.Deployment.PublicHostname)}
					}
					if e = k.Create(ctx, object); e != nil {
						return e
					}
					yes := true
					secret.OwnerReferences = []metav1.OwnerReference{{APIVersion: funnel.GroupVersion.String(), Kind: map[string]string{"controls": "FunnelControl", "gateways": "FunnelGateway", "loadbalancers": "FunnelLoadBalancer"}[kind], Name: name, UID: object.GetUID(), Controller: &yes}}
					secret.Labels = map[string]string{"tailscale-private-funnel.io/owner-uid": string(object.GetUID())}
					if e = k.Create(ctx, secret); e != nil {
						return fmt.Errorf("resource created but bootstrap Secret failed; inspect get diagnostics: %w", e)
					}
				case "get":
					if f.Wait {
						if e = waitCR(ctx, k, object, false); e != nil {
							return e
						}
					}
					return output(cmd, object, f.Output)
				case "update":
					if f.ConfigFile != "" {
						input, err := os.Open(f.ConfigFile)
						if err != nil {
							return err
						}
						defer input.Close()
						decoder := json.NewDecoder(input)
						decoder.DisallowUnknownFields()
						var target any
						switch v := object.(type) {
						case *funnel.FunnelControl:
							target = &v.Spec
						case *funnel.FunnelLoadBalancer:
							target = &v.Spec
						case *funnel.FunnelGateway:
							target = &v.Spec
						}
						if err = decoder.Decode(target); err != nil {
							return fmt.Errorf("invalid component spec: %w", err)
						}
					}
					crSpec(object).Image = provision.ImageRepository + ":" + f.Version
					if f.DisplayName != "" {
						a := object.GetAnnotations()
						if a == nil {
							a = map[string]string{}
						}
						a["tailscale-private-funnel.io/display-name"] = f.DisplayName
						object.SetAnnotations(a)
					}
					if e = k.Update(ctx, object); e != nil {
						return e
					}
				case "delete":
					uid, revision := object.GetUID(), object.GetResourceVersion()
					if e = k.Delete(ctx, object, client.Preconditions{UID: &uid, ResourceVersion: &revision}); e != nil {
						return e
					}
				}
				if f.Wait {
					if e = waitCR(ctx, k, object, action == "delete"); e != nil {
						return e
					}
				}
				if action == "delete" {
					return output(cmd, map[string]any{"name": name, "namespace": f.Namespace, "deletion_requested": true, "deleted": f.Wait, "cleanup_pending": !f.Wait}, f.Output)
				}
				return output(cmd, object, f.Output)
			}
			parent.AddCommand(cmd)
		}
	}
	return root
}
func nativeName(name string) error { return native.ValidName(name) }

func operatorManages(d apps.Deployment, ns string) bool {
	for _, c := range d.Spec.Template.Spec.Containers {
		for i, arg := range c.Args {
			if arg == "--managed-namespace="+ns {
				return true
			}
			if arg == "--managed-namespace" && i+1 < len(c.Args) && c.Args[i+1] == ns {
				return true
			}
		}
	}
	return false
}
func operatorCommands(version string) *cobra.Command {
	root := &cobra.Command{Use: "operator", Short: "Manage the single cluster Kubernetes operator"}
	for _, action := range []string{"create", "get", "update", "delete"} {
		action := action
		f := Flags{}
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		common(cmd, &f, version)
		kubeContextFlags(cmd, &f)
		if action == "create" || action == "update" {
			cmd.Flags().StringVar(&f.PlacementFile, "placement-file", "", "operator Pod placement JSON (nodeSelector, affinity, tolerations)")
		}
		cmd.Flags().StringArrayVar(&f.ManagedNamespaces, "managed-namespace", nil, "namespace receiving operator permissions; repeatable")
		if action == "delete" {
			cmd.Flags().BoolVar(&f.Yes, "yes", false, "confirm operator removal")
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			if f.Context == "" {
				return errors.New("--context is required")
			}
			if action == "delete" && !f.Yes && !f.DryRun {
				return errors.New("delete requires --yes")
			}
			if action == "create" && len(f.ManagedNamespaces) == 0 {
				return errors.New("at least one --managed-namespace is required")
			}
			var placement *funnel.Placement
			if f.PlacementFile != "" {
				var e error
				placement, e = readPlacement(f.PlacementFile)
				if e != nil {
					return e
				}
			}
			if f.DryRun {
				return output(cmd, map[string]any{"dry_run": true, "action": action, "context": f.Context, "placement": placement, "managed_namespaces": f.ManagedNamespaces, "version": f.Version}, f.Output)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), f.Timeout)
			defer cancel()
			k, e := kubeClient(f)
			if e != nil {
				return e
			}
			var deployment apps.Deployment
			existingErr := k.Get(ctx, types.NamespacedName{Namespace: OperatorNamespace, Name: "tailscale-private-funnel-k8s-operator"}, &deployment)
			if action == "create" && existingErr == nil {
				return errors.New("operator already exists; use update")
			}
			if action != "create" && existingErr != nil {
				return existingErr
			}
			if action == "create" && !apierrors.IsNotFound(existingErr) {
				return existingErr
			}
			if action == "get" {
				e = k.Get(ctx, types.NamespacedName{Namespace: OperatorNamespace, Name: "tailscale-private-funnel-k8s-operator"}, &deployment)
				if e != nil {
					return e
				}
				return output(cmd, deployment, f.Output)
			}
			if action == "delete" || action == "update" {
				for _, kind := range []string{"controls", "gateways", "loadbalancers"} {
					list := objectList(kind)
					if e = k.List(ctx, list); e != nil {
						return e
					}
					items, e := meta.ExtractList(list)
					if e != nil {
						return e
					}
					if len(items) > 0 && action == "delete" {
						return errors.New("operator deletion refused while component CRs remain")
					}
					if action == "update" && len(f.ManagedNamespaces) > 0 {
						for _, v := range items {
							a, _ := meta.Accessor(v)
							if !contains(f.ManagedNamespaces, a.GetNamespace()) {
								return errors.New("cannot remove a managed namespace while component CRs remain")
							}
						}
					}
				}
			}
			run := func(program string, args ...string) ([]byte, error) {
				env := os.Environ()
				if f.Kubeconfig != "" {
					env = append(env, "KUBECONFIG="+f.Kubeconfig)
				}
				return commandOutput(ctx, program, args, env)
			}

			if action == "delete" {
				_, e = run("helm", "--kube-context", f.Context, "uninstall", "tailscale-private-funnel", "-n", OperatorNamespace)
				if e != nil {
					return e
				}
				return output(cmd, map[string]any{"deleted": true}, f.Output)
			}
			chartVersion := strings.TrimPrefix(f.Version, "v")
			if strings.HasPrefix(f.Version, "dev-") {
				chartVersion = "0.0.0-" + f.Version
			}
			crds, e := run("helm", "show", "crds", provision.ChartRepository, "--version", chartVersion)
			if e != nil {
				return e
			}
			apply := exec.CommandContext(ctx, "kubectl", "--context", f.Context, "apply", "--server-side", "--field-manager=tailscale-private-funnel-crds", "-f", "-")
			apply.Stdin = strings.NewReader(string(crds))
			if f.Kubeconfig != "" {
				apply.Env = append(os.Environ(), "KUBECONFIG="+f.Kubeconfig)
			}
			if b, e := apply.CombinedOutput(); e != nil {
				return fmt.Errorf("CRD upgrade failed: %s", b)
			}
			for _, ns := range f.ManagedNamespaces {
				var namespace core.Namespace
				if e = k.Get(ctx, types.NamespacedName{Name: ns}, &namespace); apierrors.IsNotFound(e) {
					if e = k.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); e != nil {
						return e
					}
				} else if e != nil {
					return e
				}
			}
			values := map[string]any{"image": map[string]string{"reference": provision.ImageRepository + ":" + f.Version}}
			if placement != nil {
				values["placement"] = placement
			}
			if len(f.ManagedNamespaces) > 0 {
				values["managedNamespaces"] = f.ManagedNamespaces
			}
			data, _ := json.Marshal(values)
			verb := "install"
			if action == "update" {
				verb = "upgrade"
			}
			args := []string{"--kube-context", f.Context, verb, "tailscale-private-funnel", provision.ChartRepository, "--version", chartVersion, "-n", OperatorNamespace, "--create-namespace", "--values", "-"}
			if action == "update" {
				args = append(args, "--reuse-values")
			}
			if f.Wait {
				args = append(args, "--wait", "--timeout", f.Timeout.String())
			}
			c := exec.CommandContext(ctx, "helm", args...)
			c.Stdin = strings.NewReader(string(data))
			if f.Kubeconfig != "" {
				c.Env = append(os.Environ(), "KUBECONFIG="+f.Kubeconfig)
			}
			if b, e := c.CombinedOutput(); e != nil {
				return fmt.Errorf("operator installation failed: %s", b)
			}
			return output(cmd, map[string]any{"context": f.Context, "namespace": OperatorNamespace, "version": f.Version, "managed_namespaces": f.ManagedNamespaces}, f.Output)
		}
		root.AddCommand(cmd)
	}
	return root
}
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Manifest-producing commands must never merge registry progress on stderr into YAML stdout.
func commandOutput(ctx context.Context, program string, args, env []string) ([]byte, error) {
	c := exec.CommandContext(ctx, program, args...)
	c.Env = env
	var stderr bytes.Buffer
	c.Stderr = &stderr
	b, e := c.Output()
	if e != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", program, e, strings.TrimSpace(stderr.String()))
	}
	return b, nil
}

// Placement files cannot carry credentials or arbitrary Helm values.
func readPlacement(path string) (*funnel.Placement, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var p funnel.Placement
	d := json.NewDecoder(file)
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return nil, fmt.Errorf("invalid Pod placement: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("Pod placement requires one JSON object")
	}
	return &p, nil
}
