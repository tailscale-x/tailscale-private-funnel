package operator

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Component templates are embedded implementation details; the public chart installs only the operator.
//
//go:embed all:resourcechart
var resourceChart embed.FS

func blueprint() (*chart.Chart, error) {
	files := []*loader.BufferedFile{}
	err := fs.WalkDir(resourceChart, "resourcechart", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := resourceChart.ReadFile(path)
		if e == nil {
			files = append(files, &loader.BufferedFile{Name: strings.TrimPrefix(path, "resourcechart/"), Data: b})
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	return loader.LoadFiles(files)
}
func component(o client.Object) (string, *funnel.ComponentSpec, *funnel.ComponentStatus) {
	switch v := o.(type) {
	case *funnel.FunnelControl:
		return "control", &v.Spec.ComponentSpec, &v.Status
	case *funnel.FunnelLoadBalancer:
		return "loadbalancer", &v.Spec.ComponentSpec, &v.Status
	case *funnel.FunnelGateway:
		return "k8s-gateway", &v.Spec.ComponentSpec, &v.Status
	}
	panic("unknown Funnel component")
}
func identityName(o client.Object) string {
	mode, s, _ := component(o)
	if s.IdentitySecretRef != nil {
		return s.IdentitySecretRef.Name
	}
	if mode == "k8s-gateway" {
		return o.GetName() + "-agent-state"
	}
	return o.GetName() + "-identity"
}
func labels(o client.Object) map[string]string {
	return map[string]string{"tailscale-private-funnel.io/owner-uid": string(o.GetUID()), "app.kubernetes.io/part-of": "tailscale-private-funnel"}
}
func Render(o client.Object) ([]client.Object, error) {
	mode, s, _ := component(o)
	placement := placementValues(s.Placement)
	bootstrap := o.GetName() + "-bootstrap"
	if s.BootstrapSecretRef != nil {
		bootstrap = s.BootstrapSecretRef.Name
	}
	values := map[string]any{"component": mode, "installation": s.Installation, "image": map[string]any{"reference": s.Image}, "bootstrap": map[string]any{"existingSecret": bootstrap}, "resources": s.Resources, "identity": map[string]any{"existingSecret": identityName(o)}, "persistence": map[string]any{"existingClaim": s.Storage.ExistingClaim, "size": s.Storage.Size, "storageClass": s.Storage.StorageClassName}}
	if values["installation"] == "" {
		values["installation"] = "main"
	}
	if s.Storage.Size == "" {
		values["persistence"].(map[string]any)["size"] = "1Gi"
	}
	if len(s.Resources.Requests) == 0 && len(s.Resources.Limits) == 0 {
		delete(values, "resources")
	}
	switch v := o.(type) {
	case *funnel.FunnelControl:
		if v.Spec.Database.Type == "postgres" {
			pg := v.Spec.Database.Postgres
			if pg == nil || pg.ConnectionSecretRef.Name == "" || pg.ConnectionSecretRef.Key == "" {
				return nil, fmt.Errorf("PostgreSQL connection Secret and key are required")
			}
			if pg.ConnectionSecretRef.Name == identityName(o) || (pg.CASecretRef != nil && pg.CASecretRef.Name == identityName(o)) {
				return nil, fmt.Errorf("database credentials and identity must use different Secrets")
			}
		}
		placement["hostname"] = s.Hostname
		placement["tailnet"] = v.Spec.Tailnet
		placement["database"] = v.Spec.Database
		values["control"] = placement
	case *funnel.FunnelLoadBalancer:
		placement["service"] = map[string]any{"annotations": v.Spec.PublicService.Annotations, "loadBalancerClass": v.Spec.PublicService.LoadBalancerClass, "loadBalancerSourceRanges": v.Spec.PublicService.SourceRanges, "externalTrafficPolicy": v.Spec.PublicService.ExternalTrafficPolicy}
		if v.Spec.PublicService.ExternalTrafficPolicy == "" {
			placement["service"].(map[string]any)["externalTrafficPolicy"] = "Local"
		}
		values["loadbalancer"] = placement
	case *funnel.FunnelGateway:
		if s.IdentitySecretRef != nil {
			return nil, fmt.Errorf("gateway identity Secret names are operator-managed")
		}
		values["controller"] = placement
		values["gatewayClass"] = v.Spec.GatewayClass
		values["ingressClass"] = v.Spec.IngressClass
		values["watchNamespaces"] = v.Spec.WatchNamespaces
		forwarder := placementValues(v.Spec.ForwarderPlacement)
		if len(v.Spec.ForwarderResources.Requests) > 0 || len(v.Spec.ForwarderResources.Limits) > 0 {
			forwarder["resources"] = v.Spec.ForwarderResources
		}
		values["forwarder"] = forwarder
	}
	c, err := blueprint()
	if err != nil {
		return nil, err
	}
	normalized, _ := json.Marshal(values)
	_ = json.Unmarshal(normalized, &values)
	render, err := chartutil.ToRenderValues(c, values, chartutil.ReleaseOptions{Name: o.GetName(), Namespace: o.GetNamespace(), Revision: 1, IsInstall: true}, nil)
	if err != nil {
		return nil, err
	}
	manifests, err := engine.Render(c, render)
	if err != nil {
		return nil, err
	}
	objects := []client.Object{}
	names := make([]string, 0, len(manifests))
	for n := range manifests {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		content := manifests[name]
		decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(content), 4096)
		for {
			var object unstructured.Unstructured
			err = decoder.Decode(&object)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if object.GetKind() == "" {
				continue
			}
			if object.GetNamespace() == "" && !clusterScoped(object.GetKind()) {
				object.SetNamespace(o.GetNamespace())
			}
			ls := object.GetLabels()
			if ls == nil {
				ls = map[string]string{}
			}
			for k, v := range labels(o) {
				ls[k] = v
			}
			object.SetLabels(ls)
			if object.GetKind() == "GatewayClass" {
				_ = unstructured.SetNestedMap(object.Object, map[string]any{"group": funnel.GroupVersion.Group, "kind": "FunnelGateway", "name": o.GetName(), "namespace": o.GetNamespace()}, "spec", "parametersRef")
			}
			// Propagate ownership into Pods and runtime-created identity Secrets.
			templatePath := []string{"spec", "template", "metadata", "labels"}
			if object.GetKind() == "Deployment" || object.GetKind() == "DaemonSet" || object.GetKind() == "Job" {
				podLabels, _, _ := unstructured.NestedStringMap(object.Object, templatePath...)
				if podLabels == nil {
					podLabels = map[string]string{}
				}
				podLabels[OwnerLabel] = string(o.GetUID())
				_ = unstructured.SetNestedStringMap(object.Object, podLabels, templatePath...)
				for _, field := range []string{"containers", "initContainers"} {
					containers, found, _ := unstructured.NestedSlice(object.Object, "spec", "template", "spec", field)
					if !found {
						continue
					}
					for i, item := range containers {
						c := item.(map[string]any)
						env, _ := c["env"].([]any)
						c["env"] = append(env, map[string]any{"name": "FUNNEL_OWNER_UID", "value": string(o.GetUID())}, map[string]any{"name": "FUNNEL_RESOURCE_NAME", "value": o.GetName()}, map[string]any{"name": "FUNNEL_RESOURCE_NAMESPACE", "value": o.GetNamespace()}, map[string]any{"name": "FUNNEL_DEPLOYMENT_TARGET", "value": "kubernetes"})
						containers[i] = c
					}
					_ = unstructured.SetNestedSlice(object.Object, containers, "spec", "template", "spec", field)
				}
			}
			objects = append(objects, &object)
		}
	}
	if len(objects) == 0 {
		return nil, fmt.Errorf("component rendered no resources")
	}
	return objects, nil
}
func clusterScoped(kind string) bool {
	switch kind {
	case "GatewayClass", "IngressClass", "ClusterRole", "ClusterRoleBinding":
		return true
	}
	return false
}

func placementValues(p funnel.Placement) map[string]any {
	v := map[string]any{"nodeSelector": map[string]string{}, "affinity": map[string]any{}, "tolerations": []any{}}
	if p.NodeSelector != nil {
		v["nodeSelector"] = p.NodeSelector
	}
	if p.Affinity != nil {
		v["affinity"] = p.Affinity
	}
	if p.Tolerations != nil {
		v["tolerations"] = p.Tolerations
	}
	return v
}
