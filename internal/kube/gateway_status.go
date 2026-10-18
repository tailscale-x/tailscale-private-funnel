package kube

import (
	"context"
	"slices"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"sigs.k8s.io/controller-runtime/pkg/client"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Hosts retain route IDs (parallel to their rules), not parent Gateway IDs.
// Aggregate Gateway publication through authorized, matching route attachments.
func (c *Controller) hostReferencesObject(ctx context.Context, object client.Object, host nodeconfig.KubernetesHost, result Result) bool {
	if slices.Contains(host.ResourceIDs, resourceID(object)) {
		return true
	}
	gw, ok := object.(*v1.Gateway)
	if !ok {
		return false
	}
	for _, candidate := range result.Objects {
		route, ok := candidate.(*v1.HTTPRoute)
		if !ok || result.Problems[resourceID(route)] != "" || !slices.Contains(host.ResourceIDs, resourceID(route)) {
			continue
		}
		for _, parent := range result.Parents[resourceID(route)] {
			ns := route.Namespace
			if parent.Namespace != nil {
				ns = string(*parent.Namespace)
			}
			if ns != gw.Namespace || string(parent.Name) != gw.Name {
				continue
			}
			for _, listener := range gw.Spec.Listeners {
				if parent.SectionName != nil && *parent.SectionName != listener.Name || parent.Port != nil && *parent.Port != listener.Port {
					continue
				}
				if listener.Protocol != "HTTP" || listener.Port != 80 || listener.TLS != nil {
					continue
				}
				if listener.Hostname != nil && string(*listener.Hostname) != host.Hostname {
					continue
				}
				if allowedRoute(ctx, c.Client, *gw, listener, *route) == nil {
					return true
				}
			}
		}
	}
	return false
}
