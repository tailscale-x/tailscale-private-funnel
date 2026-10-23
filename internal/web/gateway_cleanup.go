package web

import (
	"context"
	"fmt"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
)

// cleanupRevokedNode touches only this enrolled identity and application-owned
// Services. Failures remain pending and can be retried by the control loop.
type nodeCleanupState struct {
	Pending             bool     `json:"pending"`
	Errors              []string `json:"errors,omitempty"`
	AwaitingAck         bool     `json:"awaiting_ack,omitempty"`
	OperationalComplete bool     `json:"operational_complete,omitempty"`
	DeleteAfter         int64    `json:"delete_after,omitempty"`
}

func (c *Control) cleanupRevokedNode(ctx context.Context, n store.NodeRecord) error {
	c.shutdownMu.Lock()
	defer c.shutdownMu.Unlock()
	return c.cleanupRevokedNodeLocked(ctx, n)
}
func (c *Control) cleanupRevokedNodeLocked(ctx context.Context, n store.NodeRecord) error {
	var state nodeCleanupState
	_ = c.Store.GetJSON("node_cleanup:"+n.ID, &state)

	if !n.Revoked {
		return fmt.Errorf("node must be revoked before cleanup")
	}
	if reg, e := c.Store.ComponentRegistration(n.ID); e == nil && reg.Runtime == tunnel.Runtime {
		if _, e = c.Store.WithContext(ctx).RetireLocalTunnel(n.ID, 0); e != nil {
			return e
		}
	}
	if n.Mode == "k8s-gateway" {
		if e := c.withdrawKubernetesGateway(n.ID); e != nil {
			return e
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
			_ = c.Store.SetJSON("node_cleanup:"+child.ID, nodeCleanupState{Pending: true})
			if e = c.cleanupRevokedNodeLocked(ctx, child); e != nil {
				return e
			}
		}
	}
	_ = c.Store.RevokeBackends(n.ID)
	_ = c.Store.DeleteReport(n.ID)
	failures := []string{}

	if e := c.reconcileNodeExposures(ctx, &n); e != nil {
		failures = append(failures, e.Error())
	}

	if len(failures) == 0 && n.Mode == "k8s-gateway" {
		if e := c.retireKubernetesGateway(ctx, n); e != nil {
			failures = append(failures, e.Error())
		}
	}
	if len(failures) > 0 {
		state.Pending = true
		state.Errors = failures
		_ = c.Store.SetJSON("node_cleanup:"+n.ID, state)
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	state.Pending = false
	state.Errors = nil
	state.OperationalComplete = true
	_ = c.Store.SetJSON("node_cleanup:"+n.ID, state)
	return nil
}

// RetryNodeCleanup runs independently so slow cleanup cannot starve Service approval.
func (c *Control) RetryNodeCleanup(ctx context.Context) {
	for _, mode := range []string{"gateways", "loadbalancer", "k8s-forwarder"} {
		nodes, err := c.Store.Nodes(mode)
		if err != nil {
			continue
		}
		for _, item := range nodes {
			if ctx.Err() != nil {
				return
			}
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
