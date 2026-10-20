package web

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
)

func (s *nodeRPC) SubmitDockerInventory(ctx context.Context, r *connect.Request[v1.DockerInventoryPayload]) (*connect.Response[v1.Empty], error) {
	env, err := s.c.requestSnapshot(ctx, "docker-inventory", r.Msg)
	if err == nil {
		err = s.c.acceptDockerInventory(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, err)
}
func (c *Control) acceptDockerInventory(ctx context.Context, env report.Snapshot) error {
	n, err := c.callerNodeContext(ctx, "docker-gateway")
	if err != nil {
		return rpcFailure(connect.CodePermissionDenied, err)
	}
	if env.NodeID != n.ID {
		return rpcFailure(connect.CodePermissionDenied, errors.New("Docker inventory identity mismatch"))
	}
	if err = env.Validate(time.Now(), 5*time.Minute); err != nil {
		return rpcFailure(connect.CodeUnauthenticated, err)
	}
	var p v1.DockerInventoryPayload
	if err = protojson.Unmarshal(env.Payload, &p); err != nil {
		return err
	}
	if !p.Complete || p.Revision <= 0 || len(p.Containers) > 10000 || len(env.Payload) > 4<<20 {
		return rpcFailure(connect.CodeInvalidArgument, errors.New("complete bounded Docker inventory required"))
	}
	d, err := c.nodeDeployment(n.ID)
	if err != nil {
		return err
	}
	canonicalKeys := func(keys []string) string {
		m := map[string]bool{gateway.ExposureLabel: true}
		for _, k := range keys {
			m[strings.TrimSpace(k)] = true
		}
		v := []string{}
		for k := range m {
			if k != "" {
				v = append(v, k)
			}
		}
		sort.Strings(v)
		return strings.Join(v, "\n")
	}
	if canonicalKeys(p.LabelKeys) != canonicalKeys(splitValues(d.LabelKeys)) {
		return rpcFailure(connect.CodeFailedPrecondition, errors.New("Docker label configuration differs; inventory cannot retire registrations"))
	}
	inv := store.DockerInventory{Revision: p.Revision}
	seen := map[string]bool{}
	for _, container := range p.Containers {
		if container == nil || container.Id == "" || len(container.Id) > 128 || seen[container.Id] || len(container.Labels) > 256 {
			return rpcFailure(connect.CodeInvalidArgument, errors.New("invalid inventory container"))
		}
		seen[container.Id] = true
		for k, v := range container.Labels {
			if len(k) > 256 || len(v) > 16384 {
				return rpcFailure(connect.CodeInvalidArgument, errors.New("inventory label too long"))
			}
		}
		specs, e := gateway.ParseLabels(container.Labels, splitValues(d.LabelKeys))
		if e != nil {
			return rpcFailure(connect.CodeInvalidArgument, fmt.Errorf("invalid Docker declaration: %w", e))
		}
		item := store.DockerContainer{ID: container.Id, Running: container.Running}
		for _, sp := range specs {
			item.Routes = append(item.Routes, store.DockerDeclaration{Hostname: sp.Hostname, Port: sp.Port, PathPrefix: sp.PathPrefix, Rewrite: sp.Rewrite})
		}
		inv.Containers = append(inv.Containers, item)
	}
	// Serialize only local inventory/report writes, never external provisioning.
	unlock, err := c.lockReport(ctx, n.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if err = ctx.Err(); err != nil {
		return err
	}
	_, err = c.Store.WithContext(ctx).AcceptDockerInventory(n.ID, env.Nonce, inv)
	if err != nil {
		return rpcFailure(connect.CodeAborted, err)
	}
	return nil
}
