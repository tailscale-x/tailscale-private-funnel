package web

import (
	"context"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type managementRPC struct {
	c *Control
}

func providerMessage(p store.ProviderRecord) *v1.Provider {
	return &v1.Provider{Id: p.ID, Name: p.Name, Vendor: p.Provider, AccountId: p.AccountID, Enabled: p.Enabled, Capabilities: p.Capabilities, TestedAt: p.TestedAt}
}
func (c *Control) nodeMessage(n store.NodeRecord) *v1.Node {
	d, _ := c.nodeDeployment(n.ID)
	out := &v1.Node{Id: n.ID, Name: n.Name, Mode: n.Mode, Hostname: n.Hostname, TailnetId: n.TailnetID, Revoked: n.Revoked, LastReport: n.LastReport, ParentId: n.ParentID}
	if d.ResourceName != "" {
		out.Deployment = &v1.DeploymentMetadata{Target: d.DeploymentTarget, ResourceName: d.ResourceName, Namespace: d.KubernetesNamespace, Version: d.Version, StateDir: d.StateDir, DataDir: d.DataDir, ContextHint: d.ContextHint}
	}
	var health struct {
		EffectiveTags      []string `json:"effective_tags"`
		Revision           string   `json:"revision"`
		Listeners          []string `json:"listeners"`
		Backends           uint32   `json:"backends"`
		DNSError           string   `json:"dns_sync_error"`
		Errors             []string `json:"errors"`
		NodeName           string   `json:"node_name"`
		LocalEndpoints     uint32   `json:"local_endpoints"`
		WithdrawalReason   string   `json:"withdrawal_reason"`
		AdvertisedServices []string `json:"advertised_services"`
	}
	_ = c.Store.GetJSON("node_health:"+n.ID, &health)
	out.EffectiveTags = health.EffectiveTags
	out.ConfigRevision = health.Revision
	out.Listeners = uint32(len(health.Listeners))
	out.Backends = health.Backends
	out.Diagnostics = append(out.Diagnostics, health.Errors...)
	if n.Mode == "k8s-forwarder" {
		out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("%s: %d local endpoints; %d advertised Services", health.NodeName, health.LocalEndpoints, len(health.AdvertisedServices)))
	}
	if health.WithdrawalReason != "" {
		out.Diagnostics = append(out.Diagnostics, health.WithdrawalReason)
	}
	if health.DNSError != "" {
		out.Diagnostics = append(out.Diagnostics, health.DNSError)
	}
	if n.Mode == "loadbalancer" {
		ips, retaining := c.edgeIPs(n)
		out.PublicIpv4 = ips
		if retaining {
			out.Diagnostics = append(out.Diagnostics, "Public endpoint discovery is degraded; prior owned addresses are retained.")
		}
		out.PublicReady = !n.Revoked && c.edgeFresh(n) && !retaining && len(ips) > 0
	}
	return out
}

func zoneMessage(z store.ManagedZone) *v1.Zone {
	return &v1.Zone{Id: z.ID, ProviderId: z.ProviderID, Domain: z.Zone}
}
func (s *managementRPC) WhoAmI(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Identity], error) {
	p := rpcPrincipal(ctx)
	a, _ := ctx.Value(accessKey{}).(Access)
	roles := []string{}
	if a.Viewer {
		roles = append(roles, "viewer")
	}
	if a.Operator {
		roles = append(roles, "operator")
	}
	if a.Admin {
		roles = append(roles, "admin")
	}
	return connect.NewResponse(&v1.Identity{NodeId: p.NodeID, Hostname: p.Hostname, Login: p.Login, Tags: p.Tags, Roles: roles}), nil
}
func (s *managementRPC) Health(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.HealthResponse], error) {
	p, e := s.c.Store.Providers()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	z, e := s.c.Store.Zones()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	n, e := s.c.Store.Nodes("")
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	a, e := s.c.Store.Applications()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	activeApplications := uint32(0)
	for _, app := range a {
		if app.BackendSource != "docker" || len(app.PublicHostnames) > 0 {
			activeApplications++
		}
	}
	return connect.NewResponse(&v1.HealthResponse{Version: s.c.BuildVersion, DatabaseBackend: s.c.Store.Backend(), DatabaseReady: s.c.Store.Ping(ctx) == nil, Deployment: &v1.DeploymentMetadata{Target: os.Getenv("FUNNEL_DEPLOYMENT_TARGET"), ResourceName: os.Getenv("FUNNEL_RESOURCE_NAME"), Namespace: os.Getenv("FUNNEL_RESOURCE_NAMESPACE"), Version: s.c.BuildVersion}, Providers: uint32(len(p)), Zones: uint32(len(z)), Nodes: uint32(len(n)), Applications: activeApplications}), nil
}
func (s *managementRPC) ListProviders(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Providers], error) {
	items, e := s.c.Store.Providers()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.Providers{}
	for _, p := range items {
		out.Items = append(out.Items, providerMessage(p))
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) SaveProvider(ctx context.Context, r *connect.Request[v1.SaveProviderRequest]) (*connect.Response[v1.Provider], error) {
	m := r.Msg
	p, e := s.c.saveProviderOperation(m.Id, providerInput{Name: m.Name, Provider: m.Vendor, AccountID: m.AccountId, Config: m.Credentials, Enabled: m.Enabled}, rpcActor(ctx))
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(providerMessage(p)), nil
}
func (s *managementRPC) DeleteProvider(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.Empty], error) {
	e := s.c.Store.DeleteProvider(r.Msg.Id)
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeFailedPrecondition, e)
}
func (s *managementRPC) EnableProvider(ctx context.Context, r *connect.Request[v1.EnableRequest]) (*connect.Response[v1.Empty], error) {
	if _, _, e := s.c.Store.ProviderConfig(r.Msg.Id); e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	e := s.c.Store.SetProviderEnabled(r.Msg.Id, r.Msg.Enabled)
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInternal, e)
}
func (s *managementRPC) TestProvider(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.TestResult], error) {
	p, a, e := s.c.providerAdapter(r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	_, e = s.c.discover(ctx, p.ID)
	if e != nil {
		_ = s.c.Store.SetJSON("provider_test:"+p.ID, e.Error())
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	if e = s.c.Store.MarkProviderTested(p.ID, a.Capabilities()); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	message := "Read access verified. Write access was not tested."
	_ = s.c.Store.SetJSON("provider_test:"+p.ID, message)
	return connect.NewResponse(&v1.TestResult{Ok: true, Message: message}), nil
}
func (s *managementRPC) DiscoverZones(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.DiscoveredZones], error) {
	items, e := s.c.discover(ctx, r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	zones, e := s.c.Store.Zones()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	managed := map[string]bool{}
	for _, z := range zones {
		managed[z.Zone] = true
	}
	out := &v1.DiscoveredZones{}
	for _, z := range items {
		domain := strings.ToLower(strings.TrimSuffix(z.Name, "."))
		out.Items = append(out.Items, &v1.DiscoveredZone{Domain: domain, Managed: managed[domain]})
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) ListZones(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Zones], error) {
	items, e := s.c.Store.Zones()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.Zones{}
	for _, z := range items {
		out.Items = append(out.Items, zoneMessage(z))
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) AddZone(ctx context.Context, r *connect.Request[v1.AddZoneRequest]) (*connect.Response[v1.Zone], error) {
	z, e := s.c.addZoneOperation(ctx, r.Msg.ProviderId, r.Msg.Domain, rpcActor(ctx))
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(zoneMessage(z)), nil
}
func (s *managementRPC) DeleteZone(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.Empty], error) {
	z, e := s.c.Store.ZoneByID(r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	e = s.c.Store.DeleteZone(z.ProviderID, z.Zone)
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeFailedPrecondition, e)
}
func (s *managementRPC) TestZone(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.TestResult], error) {
	z, e := s.c.Store.ZoneByID(r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	_, a, e := s.c.providerAdapter(z.ProviderID)
	if e == nil {
		_, e = a.List(ctx, z.Zone)
	}
	message := "Read access verified. Write access was not tested."
	if e != nil {
		message = e.Error()
	}
	_ = s.c.Store.SetJSON("zone_test:"+z.ProviderID+":"+z.Zone, message)
	if e != nil {
		return nil, rpcFailure(connect.CodeUnavailable, e)
	}
	return connect.NewResponse(&v1.TestResult{Ok: true, Message: message}), nil
}
func (s *managementRPC) ListNodes(ctx context.Context, r *connect.Request[v1.ListNodesRequest]) (*connect.Response[v1.Nodes], error) {
	items, e := s.c.Store.Nodes(r.Msg.Mode)
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.Nodes{}
	for _, n := range items {
		node, err := s.c.Store.Node(stringValue(n, "id", ""))
		if err != nil {
			return nil, rpcFailure(connect.CodeInternal, err)
		}
		out.Items = append(out.Items, s.c.nodeMessage(node))
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) GetNode(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.Node], error) {
	n, e := s.c.Store.Node(r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	return connect.NewResponse(s.c.nodeMessage(n)), nil
}
func (s *managementRPC) RenameNode(ctx context.Context, r *connect.Request[v1.RenameRequest]) (*connect.Response[v1.Empty], error) {
	name, e := store.ValidateName(r.Msg.Name)
	if e != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, e)
	}
	if _, e = s.c.Store.Node(r.Msg.Id); e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInternal, s.c.Store.RenameResource("node", r.Msg.Id, name))
}
func (s *managementRPC) ListEnrollments(ctx context.Context, r *connect.Request[v1.ListNodesRequest]) (*connect.Response[v1.Enrollments], error) {
	items, e := s.c.Store.Enrollments(r.Msg.Mode)
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.Enrollments{}
	for _, n := range items {
		out.Items = append(out.Items, &v1.Enrollment{Id: n.ID, Name: n.Name, Mode: n.Mode, Hostname: n.Hostname, Revoked: n.Revoked, CreatedAt: n.CreatedAt})
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) GetPolicy(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Policy], error) {
	d := s.c.loadExposurePolicy()
	out := &v1.Policy{Revision: d.Revision}
	for _, r := range d.Rules {
		out.Rules = append(out.Rules, &v1.Rule{Id: r.ID, Name: r.Name, HostnameRegexes: r.HostnameRegexes, Roles: r.Roles, GatewayIds: r.GatewayIDs, Enabled: r.Enabled})
	}
	return connect.NewResponse(out), nil
}
func policyDocument(p *v1.Policy) policy.Document {
	d := policy.Document{Revision: p.Revision}
	for _, r := range p.Rules {
		d.Rules = append(d.Rules, policy.Rule{ID: r.Id, Name: r.Name, HostnameRegexes: r.HostnameRegexes, Roles: r.Roles, GatewayIDs: r.GatewayIds, Enabled: r.Enabled})
	}
	return d
}
func (s *managementRPC) ValidatePolicy(ctx context.Context, r *connect.Request[v1.Policy]) (*connect.Response[v1.TestResult], error) {
	if e := policyDocument(r.Msg).Validate(); e != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, e)
	}
	return connect.NewResponse(&v1.TestResult{Ok: true, Message: "Policy is valid; no changes saved."}), nil
}
func (s *managementRPC) TestPolicy(ctx context.Context, r *connect.Request[v1.PolicyTestRequest]) (*connect.Response[v1.PolicyTestResult], error) {
	ok, name := s.c.loadExposurePolicy().Allows(r.Msg.Hostname, r.Msg.GatewayIds, r.Msg.Roles)
	return connect.NewResponse(&v1.PolicyTestResult{Allowed: ok, MatchedRule: name}), nil
}
func (s *managementRPC) GetAccess(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.AccessSettings], error) {
	a, e := s.c.loadAccessSettings()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.AccessSettings{Capability: a.Capability, Roles: map[string]*v1.Selectors{}}
	for role, selectors := range a.Roles {
		out.Roles[role] = &v1.Selectors{Selectors: selectors}
	}
	if len(a.Admins) > 0 {
		if out.Roles["admin"] == nil {
			out.Roles["admin"] = &v1.Selectors{}
		}
		out.Roles["admin"].Selectors = append(out.Roles["admin"].Selectors, a.Admins...)
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) GetSettings(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Settings], error) {
	var settings map[string]any
	if e := s.c.Store.GetJSON("tailnet", &settings); e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	_, idErr := s.c.Store.GetSecret("tailscale_oauth_client_id")
	_, secretErr := s.c.Store.GetSecret("tailscale_oauth_client_secret")
	return connect.NewResponse(&v1.Settings{Tailnet: stringValue(settings, "name", ""), ManagementUrl: stringValue(settings, "management_url", ""), ReleaseVersion: stringValue(settings, "release_version", ""), OauthConfigured: idErr == nil && secretErr == nil}), nil
}
func (s *managementRPC) ListApplications(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Applications], error) {
	items, e := s.c.Store.Applications()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.Applications{}
	for _, a := range items {
		if a.BackendSource == "docker" && len(a.PublicHostnames) == 0 {
			continue
		}
		out.Items = append(out.Items, applicationMessage(a))
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) ListAudit(ctx context.Context, r *connect.Request[v1.AuditRequest]) (*connect.Response[v1.AuditEvents], error) {
	limit := int(r.Msg.Limit)
	if limit == 0 {
		limit = 100
	}
	items, e := s.c.Store.AuditEvents(limit)
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.AuditEvents{}
	for _, a := range items {
		out.Items = append(out.Items, &v1.AuditEvent{Action: a.Action, Actor: a.Actor, CreatedAt: a.CreatedAt})
	}
	return connect.NewResponse(out), nil
}
