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
func materialName(o client.Object) string {
	if c, ok := o.(*funnel.FunnelControl); ok && c.Spec.EncryptionSecretRef != nil {
		return c.Spec.EncryptionSecretRef.Name
	}
	return o.GetName() + "-material"
}

func labels(o client.Object) map[string]string {
	return map[string]string{"tailscale-private-funnel.io/owner-uid": string(o.GetUID()), "app.kubernetes.io/part-of": "tailscale-private-funnel"}
}
func Render(o client.Object) ([]client.Object, error) {
	mode, s, _ := component(o)
	placement := placementValues(s.Placement)
	logicalID := s.ComponentID
	if logicalID == "" {
		logicalID = string(o.GetUID())
	}
	values := map[string]any{"component": mode, "componentID": logicalID, "installation": s.Installation, "managementURL": s.ManagementURL, "hostname": s.Hostname, "image": map[string]any{"reference": s.Image}, "resources": s.Resources, "materialSecret": materialName(o), "persistence": map[string]any{"existingClaim": s.Storage.ExistingClaim, "size": s.Storage.Size, "storageClass": s.Storage.StorageClassName}}
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
			if pg.ConnectionSecretRef.Name == materialName(o) || (pg.CASecretRef != nil && pg.CASecretRef.Name == materialName(o)) {
				return nil, fmt.Errorf("database credentials and identity must use different Secrets")
			}
		}
		placement["hostname"] = s.Hostname
		placement["tailnet"] = v.Spec.Tailnet
		placement["database"] = v.Spec.Database
		placement["servicesSecret"] = v.Spec.ServicesRef.Name
		placement["usersSecret"] = v.Spec.UsersRef.Name
		values["control"] = placement
	case *funnel.FunnelLoadBalancer:
		placement["service"] = map[string]any{"annotations": v.Spec.PublicService.Annotations, "loadBalancerClass": v.Spec.PublicService.LoadBalancerClass, "loadBalancerSourceRanges": v.Spec.PublicService.SourceRanges, "externalTrafficPolicy": v.Spec.PublicService.ExternalTrafficPolicy}
		if v.Spec.PublicService.ExternalTrafficPolicy == "" {
			placement["service"].(map[string]any)["externalTrafficPolicy"] = "Local"
		}
		placement["publicHostname"] = v.Spec.PublicHostname
		placement["certificateEmail"] = v.Spec.CertificateEmail
		values["loadbalancer"] = placement
	case *funnel.FunnelGateway:
		values["routingID"] = v.Spec.RoutingID
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
			// Propagate the CR ownership binding into workload Pods.
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
						c["env"] = append(env, map[string]any{"name": "FUNNEL_OWNER_UID", "value": string(o.GetUID())})
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
