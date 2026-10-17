package web

import (
	"context"
	"fmt"
	"strings"
)

// CleanupOwnedInstallation is used by a stopped control's cleanup runtime.
// The database is retained until exact-ledger DNS cleanup and owned Services finish.
func (c *Control) CleanupOwnedInstallation(ctx context.Context) error {
	nodes, err := c.Store.Nodes("")
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, value := range nodes {
		n, e := c.Store.Node(value["id"].(string))
		if e != nil {
			return e
		}
		if !n.Revoked {
			return fmt.Errorf("active enrolled components remain")
		}
		if n.TailnetID != "" {
			known[n.TailnetID] = true
		}
	}
	parts, err := c.Store.LedgerPartitions()
	if err != nil {
		return err
	}
	owners := map[string]bool{}
	for _, p := range parts {
		owners[p.OwnerID] = true
	}
	for owner := range owners {
		if err = c.reconcilePartitions(ctx, owner, nil); err != nil {
			return fmt.Errorf("owned DNS cleanup: %w", err)
		}
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, app := range apps {
		names[app.ServiceName] = true
		for _, backend := range app.HTTPBackends {
			names[backend.ServiceName] = true
		}
	}
	if len(names) == 0 {
		return nil
	}
	ts, err := c.tailscaleRuntimeClient()
	if err != nil {
		return err
	}
	for name := range names {
		// These names are minted internally; arbitrary referenced Services are not owned.
		if !strings.HasPrefix(name, "svc:tailscale-private-funnel-app-") && !strings.HasPrefix(name, "svc:tpf-k8s-") {
			continue
		}
		hosts, e := ts.ListServiceHosts(ctx, name)
		if e != nil && !strings.Contains(e.Error(), "404") {
			return e
		}
		for _, host := range hosts {
			if !known[host.NodeID] {
				return fmt.Errorf("Service %s has a host outside this installation; refusing deletion", name)
			}
		}
		if e = ts.DeleteService(ctx, name); e != nil {
			return fmt.Errorf("owned Service cleanup: %w", e)
		}
	}
	return nil
}
