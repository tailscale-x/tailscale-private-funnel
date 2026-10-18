package web

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"

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
	clientID, e := s.c.Store.GetSecret("tailscale_oauth_client_id")
	if e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("configure Tailscale OAuth first"))
	}
	secret, e := s.c.Store.GetSecret("tailscale_oauth_client_secret")
	if e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("configure Tailscale OAuth first"))
	}
	var settings map[string]any
	if e = s.c.Store.GetJSON("tailnet", &settings); e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	pub, _, e := s.c.Store.ConfigurationKey()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	enrollment, code, e := s.c.Store.CreateEnrollment(p.Deployment.Mode, p.Deployment.Hostname)
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	success := false
	defer func() {
		if !success {
			_ = s.c.Store.RevokeEnrollment(enrollment.ID)
		}
	}()
	if e = s.c.Store.RenameResource("enrollment", enrollment.ID, func() string {
		if p.Deployment.DisplayName != "" {
			return p.Deployment.DisplayName
		}
		return p.Deployment.ResourceName
	}()); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	d := p.Deployment
	selector, _ := json.Marshal(d.NodeSelector)
	deployment := nodeDeployment{Version: p.Version, ContextHint: d.Context, DeploymentTarget: d.Target, InstallationName: d.Installation, ResourceName: d.ResourceName, StateDir: d.StateDir, DataDir: d.DataDir, CertEmail: d.CertificateEmail, LoadBalancerHostname: d.PublicHostname, LabelKeys: strings.Join(d.LabelKeys, ","), DenyNetworks: strings.Join(d.DenyNetworks, ","), WatchNamespaces: strings.Join(d.WatchNamespaces, ","), NodeSelector: string(selector), GatewayClass: d.GatewayClass, IngressClass: d.IngressClass, KubernetesNamespace: p.Namespace}
	if d.Target == "kubernetes" && d.Mode == "loadbalancer" {
		deployment.AddressSource = "kubernetes"
		deployment.PublicService = d.ResourceName + "-public"
	}
	if e = s.c.Store.SetJSON("node_deployment:"+enrollment.ID, deployment); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	key, e := s.c.tsClient(clientID, secret, stringValue(settings, "name", "")).CreateAuthKey(ctx, d.Mode, d.Hostname, []string{requiredNodeTag(d.Mode)})
	if e != nil {
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	p.Credentials = &v1.BootstrapCredentials{EnrollmentId: enrollment.ID, EnrollmentCode: code, AuthKey: key, ControlKey: base64.RawStdEncoding.EncodeToString(pub), ExpiresAt: enrollment.CreatedAt + 900}
	p.Enrollment = &v1.Enrollment{Id: enrollment.ID, Name: d.Name, Mode: d.Mode, Hostname: d.Hostname, CreatedAt: enrollment.CreatedAt}
	p.Example = selectedGatewayExample(d.Mode, deployment)

	success = true
	_ = s.c.Store.Audit("enrollment.create:"+enrollment.ID, rpcActor(ctx))
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
