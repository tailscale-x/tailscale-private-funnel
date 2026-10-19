package web

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
)

type deploymentRPC struct {
	funnelv1connect.UnimplementedDeploymentServiceHandler
	c *Control
}

func (s *deploymentRPC) Instructions(ctx context.Context, r *connect.Request[v1.DeploymentRequest]) (*connect.Response[v1.DeploymentInstructions], error) {
	in, e := s.c.deploymentInput(r.Msg)
	if e != nil {
		return nil, e
	}
	p, e := s.c.deploymentPlan(in)
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(p), nil
}
func (c *Control) deploymentPlan(in *v1.DeploymentRequest) (*v1.DeploymentInstructions, error) {
	if in.Mode != "control" && in.Mode != "operator" {
		if in.Installation != "" && in.Installation != c.installation() {
			return nil, rpcFailure(connect.CodePermissionDenied, errors.New("component installation must match this control"))
		}
		in.Installation = c.installation()
	}
	var settings map[string]any
	e := c.Store.GetJSON("tailnet", &settings)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	if in.Mode == "control" && in.Tailnet == "" {
		in.Tailnet = stringValue(settings, "name", "")
	}
	version := stringValue(settings, "release_version", defaultRelease)
	if strings.HasPrefix(c.BuildVersion, "dev-") {
		version = c.BuildVersion
	}
	if in.Version != "" {
		version = in.Version
	}
	p, e := provision.Resolve(in, stringValue(settings, "management_url", ""), version)
	return p, rpcFailure(connect.CodeInvalidArgument, e)
}
func (s *deploymentRPC) Authorize(ctx context.Context, r *connect.Request[v1.DeploymentRequest]) (*connect.Response[v1.DeploymentInstructions], error) {
	if r.Msg.NodeId != "" || r.Msg.CurrentControl || r.Msg.Mode == "control" || r.Msg.Mode == "operator" {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("control/operator bootstrap is performed on the selected deployment target"))
	}
	p, e := s.c.deploymentPlan(r.Msg)
	if e != nil {
		return nil, e
	}
	if !strings.HasPrefix(p.Version, "dev-") {
		if e = s.c.checkRelease(ctx, p.Version); e != nil {
			return nil, rpcFailure(connect.CodeFailedPrecondition, e)
		}
	}
	d := p.Deployment
	if d.Installation != s.c.installation() {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("installer installation must match this control"))
	}
	if d.TailscaleConnection == "host" || d.Target == "kubernetes" {
		return connect.NewResponse(p), nil
	}
	role := identity.Role(d.Mode)
	id, e := s.c.Store.GetSecret("tailscale_issuer_client_id")
	if e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("configure the installation auth-key issuer"))
	}
	secret, e := s.c.Store.GetSecret("tailscale_issuer_client_secret")
	if e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	var tailnet map[string]any
	if e = s.c.Store.GetJSON("tailnet", &tailnet); e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	key, e := s.c.tsClient(id, secret, stringValue(tailnet, "name", "")).CreateAuthKey(ctx, d.Mode, d.Hostname, []string{identity.Tag(s.c.installation(), role)})
	if e != nil {
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	p.Credentials = &v1.BootstrapCredentials{AuthKey: key, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	_ = s.c.Store.Audit("bootstrap.issue:"+d.Mode, rpcActor(ctx))
	return connect.NewResponse(p), nil
}

// Node actions resolve deployment handles from stored enrollment metadata, never display names.
func (c *Control) deploymentInput(in *v1.DeploymentRequest) (*v1.DeploymentRequest, error) {
	if in.CurrentControl {
		if in.NodeId != "" {
			return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("choose current control or node"))
		}
		name, target := os.Getenv("FUNNEL_RESOURCE_NAME"), os.Getenv("FUNNEL_DEPLOYMENT_TARGET")
		if name == "" || target == "" {
			return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("control deployment metadata is unavailable; inspect the deployment with its CLI"))
		}
		return &v1.DeploymentRequest{Mode: "control", Target: target, ResourceName: name, Namespace: os.Getenv("FUNNEL_RESOURCE_NAMESPACE"), Context: in.Context, Version: in.Version}, nil
	}
	if in.NodeId == "" {
		return in, nil
	}
	n, e := c.Store.Node(in.NodeId)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, errors.New("unknown node"))
	}
	d, e := c.nodeDeployment(n.ID)
	if e != nil || d.ResourceName == "" {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("deployment metadata unavailable; inspect the deployment with its CLI"))
	}
	context := in.Context
	if context == "" {
		context = d.ContextHint
	}
	return &v1.DeploymentRequest{Mode: n.Mode, Target: d.DeploymentTarget, ResourceName: d.ResourceName, Name: d.ResourceName, Hostname: n.Hostname, StateDir: d.StateDir, DataDir: d.DataDir, Namespace: d.KubernetesNamespace, Context: context, PublicHostname: d.LoadBalancerHostname, CertificateEmail: d.CertEmail, Version: in.Version}, nil
}
