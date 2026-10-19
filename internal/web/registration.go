package web

import (
	"context"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"google.golang.org/protobuf/proto"
)

type componentContext struct{ ID, Instance string }

func (c *Control) installation() string {
	if c.Installation != "" {
		return c.Installation
	}
	return "main"
}
func (s *nodeRPC) RegisterComponent(ctx context.Context, r *connect.Request[v1.RegisterComponentRequest]) (*connect.Response[v1.RegisteredComponent], error) {
	p := rpcPrincipal(ctx)
	d := r.Msg.Deployment
	if d == nil || d.Mode == "control" || d.Mode == "operator" || d.Installation != s.c.installation() {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("matching installation and runtime role required"))
	}
	if d.Target != "native" && d.Target != "kubernetes" || d.Mode == "k8s-gateway" && d.Target != "kubernetes" {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("valid component target required"))
	}
	if r.Msg.ParentId != "" && r.Msg.NodeUid == "" {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("only forwarder assignments have a parent"))
	}
	role := identity.Role(d.Mode)
	if d.Mode == "k8s-forwarder" {
		role = identity.KubernetesGateway
	}
	if e := identity.Authorize(d.Installation, role, p.Tags); e != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, e)
	}
	connection := d.TailscaleConnection
	if connection == "" {
		connection = "embedded"
	}
	if connection == "host" && d.Target == "kubernetes" {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("Kubernetes requires embedded Tailscale"))
	}
	if d.Name == "" || len([]rune(d.Name)) > 128 {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("component display name required"))
	}
	reg := store.Registration{ComponentID: r.Msg.ComponentId, InstanceID: r.Msg.InstanceId, DeviceID: p.NodeID, Installation: d.Installation, Mode: d.Mode, Name: strings.TrimSpace(d.Name), Hostname: p.Hostname, Connection: connection, ParentID: r.Msg.ParentId, Addresses: p.Addresses}
	if r.Msg.NodeUid != "" {
		if d.Mode != "k8s-gateway" || r.Msg.ParentId == "" {
			return nil, rpcFailure(connect.CodePermissionDenied, errors.New("forwarder requires its logical gateway"))
		}
		parent, e := s.c.Store.ComponentRegistration(r.Msg.ParentId)
		if e != nil || parent.Installation != d.Installation || parent.Mode != "k8s-gateway" {
			return nil, rpcFailure(connect.CodePermissionDenied, errors.New("logical gateway is unavailable"))
		}
		reg.Mode = "k8s-forwarder"
	}
	if existing, err := s.c.Store.Node(reg.ComponentID); err == nil && existing.Name != "" {
		reg.Name = existing.Name
	}
	lease, e := s.c.Store.RegisterComponent(reg, time.Now())
	if e != nil {
		if errors.Is(e, store.ErrInstanceConflict) {
			return nil, rpcFailure(connect.CodeAborted, e)
		}
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	deployment := nodeDeployment{Version: d.Version, DeploymentTarget: d.Target, ResourceName: d.ResourceName, InstallationName: d.Installation, KubernetesNamespace: d.Namespace, PublicService: d.ResourceName + "-public", LoadBalancerHostname: d.PublicHostname, CertEmail: d.CertificateEmail, LabelKeys: strings.Join(d.LabelKeys, ","), DenyNetworks: strings.Join(d.DenyNetworks, ","), StateDir: d.StateDir, DataDir: d.DataDir}
	if d.Target == "kubernetes" && d.Mode == "loadbalancer" {
		deployment.AddressSource = "kubernetes"
	}
	if e = s.c.Store.SetJSON("node_deployment:"+reg.ComponentID, deployment); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	_ = s.c.Store.SetJSON("node_policy_roles:"+reg.ComponentID, p.RoleSelectors())
	_ = s.c.Store.Audit("component.register:"+reg.ComponentID, "node:"+p.NodeID)
	return connect.NewResponse(&v1.RegisteredComponent{ComponentId: lease.ComponentID, InstanceId: lease.InstanceID, DeviceId: lease.DeviceID, Generation: lease.Generation, LeaseUntil: lease.LeaseUntil}), nil
}

func (c *Control) requestSnapshot(ctx context.Context, purpose string, payload proto.Message) (report.Snapshot, error) {
	n, e := c.callerNodeContext(ctx, "")
	if e != nil {
		return report.Snapshot{}, e
	}
	return report.NewSnapshot(purpose, n.ID, payload, time.Now())
}
