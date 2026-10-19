package web

import (
	"context"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"strings"
)

// Service deletion is external work. Never run it inside inventory acceptance.
func (c *Control) ReconcileDockerRetirements(ctx context.Context) error {
	// Provisioning and deletion of a shared Service must not race.
	if !c.provisionMu.TryLock() {
		return nil
	}
	defer c.provisionMu.Unlock()
	pending, err := c.Store.PendingDockerRetirements()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return err
	}
	client, err := c.tailscaleRuntimeClient()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	var failures []string
	for _, r := range pending {
		if seen[r.ServiceName] {
			continue
		}
		seen[r.ServiceName] = true
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Prove this exact Service was generated for a retired application.
		if r.ServiceName != "svc:tailscale-private-funnel-app-"+r.ApplicationID {
			failures = append(failures, "unrecognized retired Service ownership")
			continue
		}
		referenced := false
		for _, a := range apps {
			if len(a.PublicHostnames) == 0 {
				continue
			}
			if a.ServiceName == r.ServiceName {
				referenced = true
			}
			for _, b := range a.HTTPBackends {
				if b.ServiceName == r.ServiceName {
					referenced = true
				}
			}
		}
		if referenced {
			continue
		}
		hosts, e := client.ListServiceHosts(ctx, r.ServiceName)
		if e != nil {
			if strings.Contains(e.Error(), "404") {
				_ = c.Store.CompleteDockerRetirement(r.ServiceName)
				continue
			}
			failures = append(failures, e.Error())
			continue
		}
		unsafe := false
		withdrawn := false
		for _, h := range hosts {
			n, e := c.Store.NodeByTailnetID(h.NodeID)
			if e != nil || n.ID != r.NodeID {
				unsafe = true
				break
			}
			if h.ApprovalLevel == "approved:auto" || h.ApprovalLevel == "approved:manual" {
				if _, e = client.ApproveServiceDevice(ctx, r.ServiceName, h.NodeID, false); e != nil {
					failures = append(failures, e.Error())
					unsafe = true
					break
				}
				withdrawn = true
				_ = c.Store.UpsertServiceHost(store.ServiceHostRecord{ServiceName: r.ServiceName, DeviceID: h.NodeID, ApprovalState: "retired", Approved: false})
			}
		}
		if unsafe || withdrawn {
			continue
		}
		if e = client.DeleteService(ctx, r.ServiceName); e != nil && !strings.Contains(e.Error(), "404") {
			failures = append(failures, e.Error())
			continue
		}
		if e = c.Store.CompleteDockerRetirement(r.ServiceName); e != nil {
			return e
		}
		_ = c.Store.Audit("docker.service.cleanup:"+r.ServiceName, "control")
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}
