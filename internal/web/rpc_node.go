package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type nodeRPC struct{ c *Control }
type enrollmentRPC struct{ c *Control }

func envelopeResponse(env report.Envelope, e error) (*connect.Response[v1.SignedEnvelope], error) {
	if e != nil {
		return nil, e
	}
	wire, e := report.Wire(env)
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	response := connect.NewResponse(wire)
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}
func (s *nodeRPC) GetConfig(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.SignedEnvelope], error) {
	return envelopeResponse(s.c.nodeConfiguration(ctx))
}
func (s *nodeRPC) ResolveApplication(ctx context.Context, r *connect.Request[v1.ResolveApplicationRequest]) (*connect.Response[v1.SignedEnvelope], error) {
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
	if node.Mode == "loadbalancer" {
		if e = s.c.reconcileLoadBalancerNode(ctx, node); e != nil {
			health["dns_sync_error"] = e.Error()
		}
	}
	if e = s.c.Store.SetJSON("node_health:"+node.ID, health); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	return connect.NewResponse(&v1.Empty{}), nil
}
func (s *nodeRPC) SubmitReport(ctx context.Context, r *connect.Request[v1.SignedEnvelope]) (*connect.Response[v1.Empty], error) {
	env, e := report.FromWireTyped(r.Msg, "report", &v1.ExposureReport{})
	if e == nil {
		e = s.c.acceptExposureReport(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *nodeRPC) SubmitPeers(ctx context.Context, r *connect.Request[v1.SignedEnvelope]) (*connect.Response[v1.Empty], error) {
	env, e := report.FromWireTyped(r.Msg, "peers", &v1.PeerSnapshot{})
	if e == nil {
		e = s.c.acceptPeerSnapshot(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *nodeRPC) SubmitKubernetes(ctx context.Context, r *connect.Request[v1.SignedEnvelope]) (*connect.Response[v1.Empty], error) {
	env, e := report.FromWireTyped(r.Msg, "kubernetes", &v1.KubernetesPayload{})
	if e == nil {
		_, e = s.c.acceptKubernetesSnapshot(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *nodeRPC) SubmitPublicEndpoint(ctx context.Context, r *connect.Request[v1.SignedEnvelope]) (*connect.Response[v1.Empty], error) {
	env, e := report.FromWireTyped(r.Msg, "public-endpoint", &v1.PublicEndpointPayload{})
	if e == nil {
		e = s.c.acceptPublicEndpoint(ctx, env)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *nodeRPC) EnrollForwarder(ctx context.Context, r *connect.Request[v1.SignedEnvelope]) (*connect.Response[v1.ForwarderEnrollment], error) {
	parent, e := s.c.callerNodeContext(ctx, "k8s-gateway")
	if e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, e)
	}
	env, e := report.FromWireTyped(r.Msg, "forwarder-enrollment", &v1.ForwarderEnrollmentPayload{})
	if e != nil || env.NodeID != parent.ID || env.PublicKey != parent.PublicKey {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("signed controller request required"))
	}
	if _, _, e = report.Verify(env, time.Now(), time.Minute); e != nil {
		return nil, rpcFailure(connect.CodeUnauthenticated, e)
	}
	var in v1.ForwarderEnrollmentPayload
	if e = json.Unmarshal(env.Payload, &in); e != nil || in.NodeUid == "" || len(in.NodeUid) > 128 || len(in.Hostname) > 63 || in.Hostname == "" {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("invalid forwarder"))
	}
	key, e := base64.RawStdEncoding.DecodeString(in.PublicKey)
	if e != nil || len(key) != 32 {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("invalid signing key"))
	}
	if e = s.c.Store.AcceptNodeSnapshot(parent.ID, env.Nonce, env.PublicKey, "forwarder_enrollment:"+parent.ID, env.Payload); e != nil {
		return nil, rpcFailure(connect.CodeAborted, e)
	}
	n, e := s.c.Store.ReserveForwarder(parent.ID, in.NodeUid, in.Hostname, in.PublicKey)
	if e != nil {
		return nil, rpcFailure(connect.CodeAborted, e)
	}
	if n.TailnetID != "" {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("registered identity exists; restore its state"))
	}
	client, e := s.c.tailscaleRuntimeClient()
	if e != nil {
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	keyValue, e := client.CreateAuthKey(ctx, "k8s-forwarder", in.Hostname, []string{"tag:tailscale-private-funnel"})
	if e != nil {
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	return connect.NewResponse(&v1.ForwarderEnrollment{NodeId: n.ID, AuthKey: keyValue}), nil
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
func (s *nodeRPC) BindForwarder(ctx context.Context, r *connect.Request[v1.SignedEnvelope]) (*connect.Response[v1.Empty], error) {
	env, e := report.FromWireTyped(r.Msg, "forwarder-binding", &v1.ForwarderBindingPayload{})
	if e != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, e)
	}
	node, e := s.c.Store.Node(env.NodeID)
	p := rpcPrincipal(ctx)
	if e != nil || node.Mode != "k8s-forwarder" || node.Revoked || node.ParentID == "" || env.PublicKey != node.PublicKey || !contains(p.Tags, "tag:tailscale-private-funnel") {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("unknown forwarder"))
	}
	parent, e := s.c.Store.Node(node.ParentID)
	if e != nil || parent.Revoked {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("controller revoked"))
	}
	if _, _, e = report.Verify(env, time.Now(), time.Minute); e != nil {
		return nil, rpcFailure(connect.CodeUnauthenticated, e)
	}
	if e = s.c.Store.BindNodeIdentity(node.ID, node.PublicKey, p.NodeID, nil); e != nil {
		return nil, rpcFailure(connect.CodeAborted, e)
	}
	return connect.NewResponse(&v1.Empty{}), nil
}
func (s *nodeRPC) Shutdown(ctx context.Context, r *connect.Request[v1.ShutdownRequest]) (*connect.Response[v1.TestResult], error) {
	node, e := s.c.cleanupCallerContext(ctx)
	if e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, e)
	}
	s.c.cleanupMu.Lock()
	defer s.c.cleanupMu.Unlock()
	s.c.shutdownMu.Lock()
	defer s.c.shutdownMu.Unlock()
	var state nodeCleanupState
	_ = s.c.Store.GetJSON("node_cleanup:"+node.ID, &state)
	if r.Msg.Confirm {
		if !node.Revoked || !state.OperationalComplete {
			return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("withdraw owned state before confirming shutdown"))
		}
		state.AwaitingAck = false
		state.Pending = true
		state.DeleteAfter = time.Now().Add(3 * time.Second).Unix()
		if e = s.c.Store.SetJSON("node_cleanup:"+node.ID, state); e != nil {
			return nil, rpcFailure(connect.CodeInternal, e)
		}
		return connect.NewResponse(&v1.TestResult{Ok: true, Message: "Device deletion queued; wait for local Tailscale revocation before removing identity state."}), nil
	}
	state.AwaitingAck = true
	state.Pending = true
	if e = s.c.Store.SetJSON("node_cleanup:"+node.ID, state); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	if e = s.c.Store.RevokeNode(node.ID); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	node.Revoked = true
	if e = s.c.cleanupRevokedNodeLocked(ctx, node); e != nil {
		return connect.NewResponse(&v1.TestResult{Message: "Owned cleanup pending: " + e.Error()}), nil
	}
	return connect.NewResponse(&v1.TestResult{Ok: true, Message: "Owned routes, advertisements and DNS withdrawn; confirm receipt to delete the device."}), nil
}

func (s *enrollmentRPC) BindSigningKey(ctx context.Context, r *connect.Request[v1.BindSigningKeyRequest]) (*connect.Response[v1.Empty], error) {
	m := r.Msg
	p := rpcPrincipal(ctx)
	if !contains(p.Tags, "tag:tailscale-private-funnel") {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("shared-tag bootstrap identity required"))
	}
	if _, e := s.c.Store.RedeemEnrollment(m.EnrollmentId, m.Code); e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("invalid enrollment"))
	}
	if e := s.c.Store.BindEnrollment(m.EnrollmentId, m.Code, base64.RawStdEncoding.EncodeToString(m.PublicKey), p.NodeID, p.Hostname, p.Addresses); e != nil {
		return nil, rpcFailure(connect.CodeAborted, errors.New("enrollment already consumed or invalid"))
	}
	return connect.NewResponse(&v1.Empty{}), nil
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
	message := "Node revoked; cleanup complete."
	if e != nil {
		message = "Node revoked; cleanup pending: " + e.Error()
	}
	return connect.NewResponse(&v1.TestResult{Ok: e == nil, Message: message}), nil
}
