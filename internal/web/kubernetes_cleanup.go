package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// Called only after all children and exact owned DNS records are withdrawn.
// Claim release is last, so failures preserve ownership and retry evidence.
func (c *Control) retireKubernetesGateway(ctx context.Context, node store.NodeRecord) error {
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, err := c.Store.Applications()
	if err != nil {
		return err
	}
	targets := []store.ApplicationRecord{}
	selected := map[string]bool{}
	services := map[string]bool{}
	for _, app := range apps {
		if app.BackendSource != "kubernetes" || len(app.AllowedGatewayNodes) != 1 || app.AllowedGatewayNodes[0] != node.ID {
			continue
		}
		if app.Enabled {
			return fmt.Errorf("Kubernetes application withdrawal is pending")
		}
		targets = append(targets, app)
		selected[app.ID] = true
		for _, b := range app.HTTPBackends {
			if !strings.HasPrefix(b.ServiceName, "svc:tpf-k8s-") {
				return fmt.Errorf("Service does not match Kubernetes application ownership")
			}
			services[b.ServiceName] = true
		}
	}
	// Shared Services remain owned and available to every other application.
	for _, app := range apps {
		if selected[app.ID] {
			continue
		}
		delete(services, app.ServiceName)
		for _, b := range app.HTTPBackends {
			delete(services, b.ServiceName)
		}
	}
	if len(services) > 0 {
		ts, e := c.tailscaleRuntimeClient()
		if e != nil {
			return e
		}
		for service := range services {
			hosts, e := ts.ListServiceHosts(ctx, service)
			if e != nil && !strings.Contains(e.Error(), "HTTP 404") {
				return e
			}
			for _, host := range hosts {
				owner, e := c.Store.NodeByTailnetID(host.NodeID)
				if e != nil || owner.Mode != "k8s-forwarder" || c.Store.ForwarderParent(owner.ID) != node.ID || !owner.Revoked {
					return fmt.Errorf("Service %s has a host outside the revoked gateway; refusing deletion", service)
				}
			}
			// The application bindings were minted by control; all remaining hosts
			// were proved to be revoked children. Never delete a foreign/shared Service.
			if e = ts.DeleteService(ctx, service); e != nil && !strings.Contains(e.Error(), "HTTP 404") {
				return e
			}
		}
	}
	return c.Store.RetireKubernetesApplications(node.ID, targets)
}

// Serialize withdrawal with snapshots. A request authenticated just before
// revocation rechecks under the same lock before it can recreate intent.
func (c *Control) withdrawKubernetesGateway(id string) error {
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, e := c.Store.Applications()
	if e != nil {
		return e
	}
	for _, app := range apps {
		if app.BackendSource == "kubernetes" && len(app.AllowedGatewayNodes) == 1 && app.AllowedGatewayNodes[0] == id && app.Enabled {
			app.Enabled = false
			if e = c.Store.UpsertApplication(app); e != nil {
				return e
			}
		}
	}
	return nil
}
