package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// Delete only the Service minted for this application, after host withdrawal.
// Keep the application record on failure so cleanup never loses ownership proof.
func (c *Control) cleanupTailnetApplicationService(ctx context.Context, app store.ApplicationRecord, apps []store.ApplicationRecord) error {
	if app.ServiceName != "svc:tailscale-private-funnel-app-"+app.ID {
		return fmt.Errorf("Service does not match application ownership")
	}
	for _, other := range apps {
		if other.ID == app.ID {
			continue
		}
		if other.ServiceName == app.ServiceName {
			return nil
		}
		for _, b := range other.HTTPBackends {
			if b.ServiceName == app.ServiceName {
				return nil
			}
		}
	}
	ts, err := c.tailscaleRuntimeClient()
	if err != nil {
		return err
	}
	hosts, err := ts.ListServiceHosts(ctx, app.ServiceName)
	if err != nil && !strings.Contains(err.Error(), "404") {
		return err
	}
	for _, host := range hosts {
		node, e := c.Store.NodeByTailnetID(host.NodeID)
		if e != nil || !contains(app.AllowedGatewayNodes, node.ID) {
			return fmt.Errorf("Service has a host outside this application's ownership")
		}
		if host.ApprovalLevel == "approved:auto" || host.ApprovalLevel == "approved:manual" {
			return fmt.Errorf("Service host withdrawal is pending")
		}
	}
	return ts.DeleteService(ctx, app.ServiceName)
}
