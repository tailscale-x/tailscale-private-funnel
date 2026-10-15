package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// cleanupRevokedNode touches only this enrolled identity and application-owned
// Services. Failures remain pending and can be retried by the control loop.
func (c *Control) cleanupRevokedNode(ctx context.Context, n store.NodeRecord) error {
	if !n.Revoked {
		return fmt.Errorf("node must be revoked before cleanup")
	}
	if n.Mode == "k8s-gateway" {
		apps, e := c.Store.Applications()
		if e != nil {
			return e
		}
		for _, app := range apps {
			// Child revocation must never disable an installation's shared application.
			// Revoking the logical installation withdraws only its exclusive intent.
			if app.BackendSource == "kubernetes" && len(app.AllowedGatewayNodes) == 1 && app.AllowedGatewayNodes[0] == n.ID && app.Enabled {
				app.Enabled = false
				if e = c.Store.UpsertApplication(app); e != nil {
					return e
				}
			}
		}
		children, e := c.Store.Forwarders(n.ID)
		if e != nil {
			return e
		}
		for _, child := range children {
			if !child.Revoked {
				if e = c.Store.RevokeNode(child.ID); e != nil {
					return e
				}
				child.Revoked = true
			}
			if e = c.cleanupRevokedNode(ctx, child); e != nil {
				return e
			}
		}
	}
	_ = c.Store.RevokeBackends(n.ID)
	_ = c.Store.DeleteReport(n.ID)
	failures := []string{}
	ts, err := c.tailscaleRuntimeClient()
	if err != nil {
		failures = append(failures, err.Error())
	} else {
		apps, e := c.Store.Applications()
		if e != nil {
			return e
		}
		services := map[string]bool{}
		for _, a := range apps {
			if len(a.AllowedGatewayNodes) > 0 && !contains(a.AllowedGatewayNodes, n.ID) && !contains(a.AllowedGatewayNodes, n.ParentID) {
				continue
			}
			services[a.ServiceName] = true
			for _, b := range a.HTTPBackends {
				services[b.ServiceName] = true
			}
		}
		for service := range services {
			if !strings.HasPrefix(service, "svc:") {
				continue
			}
			hosts, e := ts.ListServiceHosts(ctx, service)
			if e != nil {
				if !strings.Contains(e.Error(), "HTTP 404") {
					failures = append(failures, e.Error())
				}
				continue
			}
			for _, h := range hosts {
				if h.NodeID == n.TailnetID && (h.ApprovalLevel == "approved:auto" || h.ApprovalLevel == "approved:manual") {
					if _, e = ts.ApproveServiceDevice(ctx, service, n.TailnetID, false); e != nil {
						failures = append(failures, e.Error())
					}
				}
			}
		}
		devices, e := ts.ListDevices(ctx)
		if e != nil {
			failures = append(failures, e.Error())
		} else {
			for _, d := range devices {
				if d.NodeID == n.TailnetID {
					if e = ts.DeleteDevice(ctx, d.ID); e != nil {
						failures = append(failures, e.Error())
					}
				}
			}
		}
	}
	if e := c.reconcileExposures(ctx); e != nil {
		failures = append(failures, e.Error())
	}
	if len(failures) > 0 {
		_ = c.Store.SetJSON("node_cleanup:"+n.ID, map[string]any{"pending": true, "errors": failures})
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	_ = c.Store.SetJSON("node_cleanup:"+n.ID, map[string]any{"pending": false})
	return nil
}

func (c *Control) retryNodeCleanup(ctx context.Context) {
	for _, mode := range []string{"gateways", "loadbalancer", "k8s-forwarder"} {
		nodes, err := c.Store.Nodes(mode)
		if err != nil {
			continue
		}
		for _, item := range nodes {
			if item["revoked"] != true {
				continue
			}
			id := fmt.Sprint(item["id"])
			var state struct {
				Pending bool `json:"pending"`
			}
			if c.Store.GetJSON("node_cleanup:"+id, &state) != nil || !state.Pending {
				continue
			}
			n, err := c.Store.Node(id)
			if err == nil {
				_ = c.cleanupRevokedNode(ctx, n)
			}
		}
	}
}
