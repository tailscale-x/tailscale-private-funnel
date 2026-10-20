package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type nodeRPC struct{ c *Control }

func envelopeResponse(env report.Snapshot, e error) (*connect.Response[v1.ConfigurationPayload], error) {
	if e != nil {
		return nil, e
	}
	wire := &v1.ConfigurationPayload{}
	if e = report.Decode(env, wire); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	response := connect.NewResponse(wire)
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}
func (s *nodeRPC) GetConfig(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.ConfigurationPayload], error) {
	start := time.Now()
	env, err := s.c.nodeConfiguration(ctx)
	if elapsed := time.Since(start); elapsed >= 3*time.Second {
		selection, _ := ctx.Value(componentContext{}).(componentContext)
		slog.Warn("slow runtime configuration", "component", selection.ID, "elapsed", elapsed, "error", err, "context_error", ctx.Err())
	}
	return envelopeResponse(env, err)
}
func (s *nodeRPC) ResolveApplication(ctx context.Context, r *connect.Request[v1.ResolveApplicationRequest]) (*connect.Response[v1.ConfigurationPayload], error) {
	return envelopeResponse(s.c.resolveApplication(ctx, r.Msg))
}
func (s *nodeRPC) Acknowledge(ctx context.Context, r *connect.Request[v1.ConfigAck]) (*connect.Response[v1.Empty], error) {
	node, e := s.c.callerNodeContext(ctx, "")
	if e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, e)
	}
	e = s.c.Store.SetJSON("node_config_ack:"+node.ID, map[string]any{"revision": r.Msg.Revision, "services": r.Msg.Services, "at": time.Now().Unix()})
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	return connect.NewResponse(&v1.Empty{}), nil
}
func (s *nodeRPC) ReportHealth(ctx context.Context, r *connect.Request[v1.NodeHealth]) (*connect.Response[v1.Empty], error) {
	node, e := s.c.callerNodeContext(ctx, "")
	if e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, e)
	}
	raw, _ := json.Marshal(r.Msg)
	var health map[string]any
	if e = json.Unmarshal(raw, &health); e != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, e)
	}
	health["received_at"] = time.Now().Unix()
	health["effective_tags"] = rpcPrincipal(ctx).Tags
	if e = s.c.Store.TouchNodeReport(node.ID); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	if e = s.c.Store.SetJSON("node_health:"+node.ID, health); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	// Endpoint discovery and DNS writes run on the control workers, never on a
	// liveness request's deadline.
	return connect.NewResponse(&v1.Empty{}), nil
}
func (s *nodeRPC) SubmitReport(ctx context.Context, r *connect.Request[v1.ExposureReport]) (*connect.Response[v1.Empty], error) {
	env, e := s.c.requestSnapshot(ctx, "report", r.Msg)
	if e == nil {
		e = s.c.acceptExposureReport(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *nodeRPC) SubmitPeers(ctx context.Context, r *connect.Request[v1.PeerSnapshot]) (*connect.Response[v1.Empty], error) {
	env, e := s.c.requestSnapshot(ctx, "peers", r.Msg)
	if e == nil {
		e = s.c.acceptPeerSnapshot(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *nodeRPC) SubmitKubernetes(ctx context.Context, r *connect.Request[v1.KubernetesPayload]) (*connect.Response[v1.Empty], error) {
	env, e := s.c.requestSnapshot(ctx, "kubernetes", r.Msg)
	if e == nil {
		_, e = s.c.acceptKubernetesSnapshot(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *nodeRPC) SubmitPublicEndpoint(ctx context.Context, r *connect.Request[v1.PublicEndpointPayload]) (*connect.Response[v1.Empty], error) {
	env, e := s.c.requestSnapshot(ctx, "public-endpoint", r.Msg)
	if e == nil {
		e = s.c.acceptPublicEndpoint(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}

func (s *nodeRPC) ListForwarders(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Nodes], error) {
	parent, e := s.c.callerNodeContext(ctx, "k8s-gateway")
	if e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, e)
	}
	items, e := s.c.Store.Forwarders(parent.ID)
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.Nodes{}
	for _, n := range items {
		out.Items = append(out.Items, s.c.nodeMessage(n))
	}
	return connect.NewResponse(out), nil
}

func (s *nodeRPC) Shutdown(ctx context.Context, r *connect.Request[v1.ShutdownRequest]) (*connect.Response[v1.TestResult], error) {
	node, e := s.c.callerNodeContext(ctx, "")
	if e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, e)
	}
	if r.Msg.Remove {
		if e = s.c.Store.RevokeNode(node.ID); e != nil {
			return nil, rpcFailure(connect.CodeInternal, e)
		}
		node.Revoked = true
		if e = s.c.cleanupRevokedNode(ctx, node); e != nil {
			return nil, rpcFailure(connect.CodeUnavailable, e)
		}
	}
	selection, _ := ctx.Value(componentContext{}).(componentContext)
	if e = s.c.Store.ReleaseInstance(node.ID, selection.Instance, rpcPrincipal(ctx).NodeID); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	if e = s.c.Store.DeleteReport(node.ID); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	if e = s.c.Store.RevokeBackends(node.ID); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	_ = s.c.Store.Audit("component.stop:"+node.ID, "node:"+rpcPrincipal(ctx).NodeID)
	return connect.NewResponse(&v1.TestResult{Ok: true, Message: "Component stopped; owned cleanup queued. Tailscale role authorization is unchanged."}), nil
}

func (s *managementRPC) revokeNode(ctx context.Context, node store.NodeRecord) (*connect.Response[v1.TestResult], error) {
	s.c.shutdownMu.Lock()
	defer s.c.shutdownMu.Unlock()
	_ = s.c.Store.SetJSON("node_cleanup:"+node.ID, nodeCleanupState{Pending: true})
	if e := s.c.Store.RevokeNode(node.ID); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	node.Revoked = true
	e := s.c.cleanupRevokedNodeLocked(ctx, node)
	message := "Component disabled; cleanup complete. Tailscale role authority is unchanged."
	if e != nil {
		message = "Component disabled; cleanup pending; Tailscale role authority is unchanged: " + e.Error()
	}
	return connect.NewResponse(&v1.TestResult{Ok: e == nil, Message: message}), nil
}
