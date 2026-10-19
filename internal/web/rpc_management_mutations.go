package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func applicationMessage(a store.ApplicationRecord) *v1.Application {
	var path string
	var rewrite *v1.PrefixRewrite
	if a.BackendSource == "docker" && len(a.HTTPRules) == 1 && len(a.HTTPRules[0].Matches) == 1 {
		path = a.HTTPRules[0].Matches[0].Path
		if a.HTTPRules[0].Rewrite != nil {
			rewrite = &v1.PrefixRewrite{ReplacePrefix: a.HTTPRules[0].Rewrite.ReplacePrefix}
		}
	}
	return &v1.Application{PathPrefix: path, Rewrite: rewrite, Id: a.ID, Name: a.Name, BackendSource: a.BackendSource, PublicHostnames: a.PublicHostnames, ServiceName: a.ServiceName, ServiceFqdn: a.ServiceFQDN, ServicePort: uint32(a.ServicePort), BackendPort: uint32(a.BackendPort), GatewayIds: a.AllowedGatewayNodes, Enabled: a.Enabled, BackendDeviceId: a.BackendDeviceID, Revision: a.PolicyRevision}
}
func policyMessage(d policy.Document) *v1.Policy {
	p := &v1.Policy{Revision: d.Revision}
	for _, r := range d.Rules {
		p.Rules = append(p.Rules, &v1.Rule{Id: r.ID, Name: r.Name, HostnameRegexes: r.HostnameRegexes, GatewayIds: r.GatewayIDs, Roles: r.Roles, Enabled: r.Enabled})
	}
	return p
}
func (s *managementRPC) ListVendors(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Vendors], error) {
	out := &v1.Vendors{}
	for _, p := range dns.Catalog {
		out.Items = append(out.Items, &v1.Vendor{Id: p.ID, Fields: p.Fields, Available: dns.Registry[p.ID] != nil})
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) SavePolicy(ctx context.Context, r *connect.Request[v1.Policy]) (*connect.Response[v1.PolicySaveResult], error) {
	d, e := s.c.savePolicyOperation(ctx, policyDocument(r.Msg), rpcActor(ctx))
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&v1.PolicySaveResult{Policy: policyMessage(d), CleanupPending: true}), nil
}
func (s *managementRPC) PreviewPolicy(ctx context.Context, r *connect.Request[v1.Policy]) (*connect.Response[v1.PolicyImpact], error) {
	d := policyDocument(r.Msg)
	if e := d.Validate(); e != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, e)
	}
	return connect.NewResponse(&v1.PolicyImpact{AffectedHostnames: s.c.policyImpact(d)}), nil
}
func (s *managementRPC) SaveRule(ctx context.Context, r *connect.Request[v1.SaveRuleRequest]) (*connect.Response[v1.PolicySaveResult], error) {
	if r.Msg.Rule == nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("rule required"))
	}
	p := policyMessage(s.c.loadExposurePolicy())
	if p.Revision != r.Msg.Revision {
		return nil, rpcFailure(connect.CodeAborted, errors.New("Policy changed; reload"))
	}
	if r.Msg.Rule.Id == "" {
		p.Rules = append(p.Rules, r.Msg.Rule)
	} else {
		found := false
		for i, rule := range p.Rules {
			if rule.Id == r.Msg.Rule.Id {
				p.Rules[i] = r.Msg.Rule
				found = true
				break
			}
		}
		if !found {
			return nil, rpcFailure(connect.CodeNotFound, errors.New("rule not found"))
		}
	}
	return s.SavePolicy(ctx, connect.NewRequest(p))
}
func (s *managementRPC) DeleteRule(ctx context.Context, r *connect.Request[v1.DeleteRuleRequest]) (*connect.Response[v1.PolicySaveResult], error) {
	p := policyMessage(s.c.loadExposurePolicy())
	if p.Revision != r.Msg.Revision {
		return nil, rpcFailure(connect.CodeAborted, errors.New("Policy changed; reload"))
	}
	found := false
	for i, rule := range p.Rules {
		if rule.Id == r.Msg.Id {
			p.Rules = append(p.Rules[:i], p.Rules[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return nil, rpcFailure(connect.CodeNotFound, errors.New("rule not found"))
	}
	return s.SavePolicy(ctx, connect.NewRequest(p))
}
func (s *managementRPC) SaveAccess(ctx context.Context, r *connect.Request[v1.AccessSettings]) (*connect.Response[v1.AccessSettings], error) {
	settings := AccessSettings{Capability: r.Msg.Capability, Roles: map[string][]string{}}
	if settings.Capability == "" {
		settings.Capability = DefaultCapability
	}
	if !strings.Contains(settings.Capability, "/") || strings.HasPrefix(settings.Capability, "tailscale.com/") || strings.HasPrefix(settings.Capability, "tailscale.io/") {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("custom application capability required"))
	}
	for role, value := range r.Msg.Roles {
		if role != "viewer" && role != "operator" && role != "admin" {
			return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("unknown control role"))
		}
		for _, selector := range value.Selectors {
			if strings.TrimSpace(selector) == "*" {
				return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("wildcard roles are not supported"))
			}
		}
		settings.Roles[role] = value.Selectors
	}
	if !settings.permissions(rpcPrincipal(ctx)).Admin {
		if !r.Msg.ConfirmSelfRemoval {
			return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("retain your administrator access, or explicitly confirm self-removal with an active Owner remaining"))
		}
		if e := s.c.verifyOwnerAdministration(ctx, settings); e != nil {
			return nil, rpcFailure(connect.CodeFailedPrecondition, e)
		}
	}
	s.c.accessMu.Lock()
	defer s.c.accessMu.Unlock()
	if e := s.c.Store.SetJSON("access_roles", settings); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	_ = s.c.Store.Audit("access.roles.save", rpcActor(ctx))
	if r.Msg.ConfirmSelfRemoval {
		_ = s.c.Store.Audit("access.self-removal", rpcActor(ctx))
	}
	r.Msg.ConfirmSelfRemoval = false
	return connect.NewResponse(r.Msg), nil
}
func (s *managementRPC) SaveDefaults(ctx context.Context, r *connect.Request[v1.DeploymentDefaults]) (*connect.Response[v1.Settings], error) {
	if e := validManagementURL(r.Msg.ManagementUrl); e != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, e)
	}
	if e := s.c.checkRelease(ctx, r.Msg.ReleaseVersion); e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	s.c.settingsMu.Lock()
	var settings map[string]any
	e := s.c.Store.GetJSON("tailnet", &settings)
	if e == nil {
		settings["management_url"] = r.Msg.ManagementUrl
		settings["release_version"] = r.Msg.ReleaseVersion
		settings["image"] = "ghcr.io/tailscale-x/tailscale-private-funnel:" + r.Msg.ReleaseVersion
		e = s.c.Store.SetJSON("tailnet", settings)
	}
	s.c.settingsMu.Unlock()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	return s.GetSettings(ctx, connect.NewRequest(&v1.Empty{}))
}
func (s *managementRPC) TestOAuth(ctx context.Context, r *connect.Request[v1.OAuthRequest]) (*connect.Response[v1.TestResult], error) {
	return s.oauth(ctx, r.Msg, false)
}
func (s *managementRPC) SaveOAuth(ctx context.Context, r *connect.Request[v1.OAuthRequest]) (*connect.Response[v1.TestResult], error) {
	return s.oauth(ctx, r.Msg, true)
}
func (s *managementRPC) ListExposures(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Exposures], error) {
	items, e := s.c.Store.Exposures()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.Exposures{}
	for _, item := range items {
		if !item.Active {
			continue
		}
		_, states := s.c.exposureDesired(item, s.c.loadExposurePolicy())
		exposure := &v1.Exposure{Id: item.ID, ApplicationId: item.ApplicationID, Hostname: item.Hostname, Active: item.Active}
		for _, edge := range states {
			ips, _ := edge["public_ipv4"].([]string)
			exposure.Edges = append(exposure.Edges, &v1.Edge{Id: stringValue(edge, "id", ""), Name: stringValue(edge, "name", ""), Ready: edge["ready"] == true, Healthy: edge["healthy"] == true, PolicyAllowed: edge["policy_allowed"] == true, BackendReachable: edge["backend_reachable"] == true, PublicIpv4: ips, Reason: stringValue(edge, "reason", "")})
		}
		out.Items = append(out.Items, exposure)
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) DisableNode(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.TestResult], error) {
	node, e := s.c.Store.Node(r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	return s.revokeNode(ctx, node)
}

func (s *managementRPC) CleanupNode(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.TestResult], error) {
	node, e := s.c.Store.Node(r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	if !node.Revoked {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("disable the component before cleanup"))
	}
	return s.revokeNode(ctx, node)
}

func (s *managementRPC) GetPeers(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.PeerSnapshot], error) {
	node, e := s.c.Store.Node(r.Msg.Id)
	if e != nil || node.Revoked || node.Mode != "tailnet-gateway" {
		return nil, rpcFailure(connect.CodeNotFound, errors.New("active tailnet gateway required"))
	}
	snapshot, e := s.c.peerSnapshot(node.ID)
	if e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	out := &v1.PeerSnapshot{Tags: snapshot.Tags, ExpiresAt: snapshot.ExpiresAt, ReceivedAt: snapshot.ReceivedAt}
	for _, p := range snapshot.Peers {
		out.Peers = append(out.Peers, &v1.Peer{Id: p.ID, Hostname: p.Hostname, DnsName: p.DNSName, Addresses: p.Addresses, Tags: p.Tags, Online: p.Online})
	}
	for _, p := range snapshot.Results {
		out.Results = append(out.Results, &v1.ProbeResult{Id: p.ID, DeviceId: p.DeviceID, Port: uint32(p.Port), Reachable: p.Reachable, Error: p.Error, CheckedAt: p.CheckedAt})
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) ProbeBackend(ctx context.Context, r *connect.Request[v1.ProbeRequest]) (*connect.Response[v1.Probe], error) {
	s.c.settingsMu.Lock()
	defer s.c.settingsMu.Unlock()
	node, e := s.c.Store.Node(r.Msg.NodeId)
	if e != nil || node.Revoked || node.Mode != "tailnet-gateway" {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("active tailnet gateway required"))
	}
	snapshot, e := s.c.peerSnapshot(node.ID)
	if e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	if _, ok := findPeer(snapshot, r.Msg.DeviceId); !ok {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("select a device visible through this gateway"))
	}
	var probes []nodeconfig.Probe
	_ = s.c.Store.GetJSON("node_probes:"+node.ID, &probes)
	pending := []nodeconfig.Probe{}
	for _, p := range probes {
		if p.ExpiresAt > time.Now().Unix() {
			pending = append(pending, p)
		}
	}
	if len(pending) >= 20 {
		return nil, rpcFailure(connect.CodeResourceExhausted, errors.New("too many pending tests"))
	}
	random := make([]byte, 12)
	if _, e = rand.Read(random); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	p := nodeconfig.Probe{ID: hex.EncodeToString(random), DeviceID: r.Msg.DeviceId, Port: uint16(r.Msg.Port), ExpiresAt: time.Now().Add(2 * time.Minute).Unix()}
	pending = append(pending, p)
	if e = s.c.Store.SetJSON("node_probes:"+node.ID, pending); e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	return connect.NewResponse(&v1.Probe{Id: p.ID, DeviceId: p.DeviceID, Port: uint32(p.Port), ExpiresAt: p.ExpiresAt}), nil
}
func (s *managementRPC) SaveTailnetExposure(ctx context.Context, r *connect.Request[v1.TailnetExposureRequest]) (*connect.Response[v1.Application], error) {
	m := r.Msg
	a, e := s.c.saveTailnetExposureOperation(ctx, m.Id, tailnetExposureInput{Name: m.Name, GatewayID: m.NodeId, DeviceID: m.BackendDeviceId, Port: uint16(m.BackendPort), Hostname: m.PublicHostname, Enabled: m.Enabled, Revision: m.Revision, ProbeID: m.ProbeId}, rpcActor(ctx))
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(applicationMessage(a)), nil
}
func (s *managementRPC) DeleteTailnetExposure(ctx context.Context, r *connect.Request[v1.ResourceRevision]) (*connect.Response[v1.TestResult], error) {
	s.c.provisionMu.Lock()
	defer s.c.provisionMu.Unlock()
	apps, e := s.c.Store.Applications()
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	for _, a := range apps {
		if a.ID != r.Msg.Id || a.BackendSource != "tailnet" {
			continue
		}
		if a.PolicyRevision != r.Msg.Revision {
			return nil, rpcFailure(connect.CodeAborted, errors.New("Exposure changed; reload"))
		}
		a.Enabled = false
		if e = s.c.Store.UpsertApplication(a); e != nil {
			return nil, rpcFailure(connect.CodeInternal, e)
		}
		if e = s.c.ReconcilePolicy(ctx); e == nil {
			e = s.c.ReconcileServiceApprovals(ctx)
		}
		if e != nil {
			return connect.NewResponse(&v1.TestResult{Message: "Exposure disabled; cleanup pending. Retry deletion after reconciliation."}), nil
		}
		if e = s.c.cleanupTailnetApplicationService(ctx, a, apps); e != nil {
			return connect.NewResponse(&v1.TestResult{Message: "Exposure disabled; owned Service cleanup pending. Retry deletion: " + e.Error()}), nil
		}
		if e = s.c.Store.DeleteApplication(a.ID); e != nil {
			return nil, rpcFailure(connect.CodeInternal, e)
		}
		return connect.NewResponse(&v1.TestResult{Ok: true, Message: "Exposure removed."}), nil
	}
	return nil, rpcFailure(connect.CodeNotFound, errors.New("tailnet exposure not found"))
}
func (s *managementRPC) AddAlias(ctx context.Context, r *connect.Request[v1.AliasRequest]) (*connect.Response[v1.Application], error) {
	a, e := s.c.addAliasOperation(ctx, r.Msg.ApplicationId, r.Msg.Hostname, rpcActor(ctx))
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(applicationMessage(a)), nil
}
