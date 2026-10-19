package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func (c *Control) callerNodeContext(ctx context.Context, mode string) (store.NodeRecord, error) {
	p := rpcPrincipal(ctx)
	if p.NodeID == "" {
		return store.NodeRecord{}, fmt.Errorf("verified tailnet node identity required")
	}
	selection, _ := ctx.Value(componentContext{}).(componentContext)
	var node store.NodeRecord
	var err error
	if selection.ID != "" {
		node, err = c.Store.Node(selection.ID)
	} else {
		node, err = c.Store.NodeByTailnetID(p.NodeID)
	}
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return node, rpcFailure(connect.CodeUnavailable, errors.New("component storage unavailable"))
		}
		return node, fmt.Errorf("unknown or ambiguous component")
	}
	registration, err := c.Store.ComponentRegistration(node.ID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return node, rpcFailure(connect.CodeUnavailable, errors.New("component storage unavailable"))
		}
		return node, fmt.Errorf("component registration unavailable")
	}
	if registration.Installation != c.installation() {
		return node, fmt.Errorf("component belongs to another installation")
	}
	if err = identity.Authorize(c.installation(), registration.Role(), p.Tags); err != nil {
		return node, err
	}
	if node.Revoked || (mode != "" && node.Mode != mode) {
		return node, fmt.Errorf("inactive or mismatched component")
	}
	if selection.Instance == "" {
		return node, fmt.Errorf("runtime instance selector required")
	}
	if err = c.Store.WithContext(ctx).RenewInstance(node.ID, selection.Instance, p.NodeID, time.Now()); err != nil {
		return node, err
	}
	verifiedHostname := strings.SplitN(strings.TrimSuffix(p.Hostname, "."), ".", 2)[0]
	if verifiedHostname != "" && verifiedHostname != node.Hostname {
		if err := c.Store.RefreshNodeHostname(node.ID, p.NodeID, p.Hostname); err != nil {
			return node, err
		}
		node, err = c.Store.Node(node.ID)
		if err != nil {
			return node, err
		}
	}
	if node.ParentID != "" {
		parent, e := c.Store.Node(node.ParentID)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return node, rpcFailure(connect.CodeUnavailable, errors.New("controller identity storage unavailable"))
		}
		if e != nil || parent.Revoked {
			return node, fmt.Errorf("controller revoked")
		}
	}
	return node, nil
}

func (c *Control) callerGatewayContext(ctx context.Context) (store.NodeRecord, error) {
	n, err := c.callerNodeContext(ctx, "")
	if err != nil {
		return n, err
	}
	if !store.IsGateway(n.Mode) {
		return n, fmt.Errorf("registered gateway required")
	}
	return n, nil
}

func (c *Control) nodeConfiguration(ctx context.Context) (report.Snapshot, error) {
	if n, err := c.callerNodeContext(ctx, "loadbalancer"); err == nil && n.Mode == "loadbalancer" {
		return c.loadBalancerConfiguration(ctx)
	}
	node, err := c.callerGatewayContext(ctx)
	id := node.ID
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New(err.Error()))
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, rpcPrincipal(ctx).RoleSelectors())
	apps, err := c.Store.Applications()
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}
	deployment, err := c.nodeDeployment(node.ID)
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(503), errors.New("Unable to read node settings"))
	}
	settings := struct{ LabelKeys string }{deployment.LabelKeys}
	filtered := make([]store.ApplicationRecord, 0)
	for _, app := range apps {
		if store.BackendSource(node.Mode) != app.BackendSource && !(node.Mode == "docker-gateway" && app.BackendSource == "") {
			continue
		}
		if !applicationAllows(app, node) {
			continue
		}
		hosts := make([]string, 0)
		for _, host := range app.PublicHostnames {
			if c.anyEdgeAllows(c.loadExposurePolicy(), host, []string{id, node.TailnetID, node.ParentID}, rpcPrincipal(ctx).RoleSelectors()) {
				if _, err := c.managedZone(host); err != nil {
					continue
				}
				hosts = append(hosts, host)
			}
		}
		// Retain source label hostnames while any alias needs the shared Service.
		app.Name = ""
		if len(hosts) > 0 {
			filtered = append(filtered, app)
		}
	}
	config := nodeconfig.Configuration{NodeID: id, Applications: filtered, LabelAliases: splitValues(settings.LabelKeys)}
	_ = c.Store.GetJSON("node_probes:"+node.ID, &config.Probes)
	config.PublishedHostnames = c.publishedGatewayHosts()
	for _, edge := range c.loadBalancers() {
		if c.edgeFresh(edge) {
			ips, _ := c.edgeIPs(edge)
			config.PublishedAddresses = append(config.PublishedAddresses, ips...)
		}
	}
	config.Revision = nodeconfig.Revision(config)
	config.IssuedAt = time.Now().Unix()
	envelope, err := report.NewSnapshot("configuration", id, config, time.Now())
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}

	return envelope, nil
}

func (c *Control) loadBalancerConfiguration(ctx context.Context) (report.Snapshot, error) {
	node, err := c.callerNodeContext(ctx, "loadbalancer")
	if err != nil || node.Revoked || node.Mode != "loadbalancer" {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New("registered load balancer identity required"))
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}
	routes := []proxy.Route{}
	config := nodeconfig.Configuration{NodeID: node.ID}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, rpcPrincipal(ctx).RoleSelectors())
	authorized := c.authorizedReports(c.loadExposurePolicy())
	for _, app := range apps {
		if !app.Enabled {
			continue
		}
		hosts := []string{}
		for _, host := range app.PublicHostnames {
			if !c.selected(host, node.ID) {
				continue
			}
			if _, err := c.managedZone(host); err != nil {
				continue
			}
			eligible := false
			healthy := false
			for id, exposures := range authorized {
				ing, err := c.Store.Node(id)
				if err != nil || !store.IsGateway(ing.Mode) {
					continue
				}
				var roles []string
				_ = c.Store.GetJSON("node_policy_roles:"+id, &roles)
				for _, e := range exposures {
					if e.ApplicationID == app.ID && e.PublicHostname == host {
						if ok, _ := c.loadExposurePolicy().Allows(host, []string{id, ing.TailnetID}, roles); ok {
							eligible = true
							healthy = c.edgeReported(node, app, host)
						}
					}
				}
			}
			if !eligible {
				continue
			}
			hosts = append(hosts, host)
			if healthy && c.edgeFresh(node) {
				routes = append(routes, proxy.Route{Hostname: host, Service: app.ServiceFQDN, Port: app.ServicePort, Rules: app.HTTPRules})
			}
		}
		if len(hosts) > 0 {
			app.PublicHostnames = hosts
			app.BackendAddresses = nil
			config.Applications = append(config.Applications, app)
		}
	}
	config.Routes = routes
	_ = c.Store.GetJSON("node_probes:"+node.ID, &config.Probes)
	config.Revision = nodeconfig.Revision(config)
	config.IssuedAt = time.Now().Unix()
	_ = c.Store.SetJSON("node_sent_config:"+node.ID, map[string]any{"revision": config.Revision, "routes": routes})
	envelope, err := report.NewSnapshot("configuration", node.ID, config, time.Now())
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}

	return envelope, nil
}

func (c *Control) acceptDirectReport(ctx context.Context, env report.Snapshot, node store.NodeRecord) error {
	if node.Revoked || node.TailnetID == "" || rpcPrincipal(ctx).NodeID != node.TailnetID {
		return rpcFailure(connectCodeHTTP(403), errors.New("load balancer identity mismatch"))
	}
	var p struct {
		Exposures []dns.GatewayExposure `json:"exposures"`
		ExpiresAt int64                 `json:"expires_at"`
	}
	if json.Unmarshal(env.Payload, &p) != nil || p.ExpiresAt <= time.Now().Unix() || p.ExpiresAt > env.Timestamp+300 {
		return rpcFailure(connectCodeHTTP(400), errors.New("invalid report expiry"))
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}
	for _, e := range p.Exposures {
		valid := false
		for _, app := range apps {
			if app.Enabled && app.ID == e.ApplicationID && app.BackendPort == e.Port && contains(app.PublicHostnames, e.PublicHostname) && c.selected(e.PublicHostname, node.ID) && app.ServiceName == e.ServiceName && e.ContainerID == "" {
				valid = true
				break
			}
		}
		if !valid {
			return rpcFailure(connectCodeHTTP(403), errors.New("exposure is not assigned to this load balancer"))
		}
	}

	encoded, _ := json.Marshal(env)
	if err = c.Store.AcceptReport(node.ID, env.Nonce, encoded); err != nil {
		status := 500
		if strings.Contains(err.Error(), "nonce already") {
			status = 409
		}
		return rpcFailure(connectCodeHTTP(status), errors.New(err.Error()))
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, rpcPrincipal(ctx).RoleSelectors())
	return nil
}

func (c *Control) acceptExposureReport(ctx context.Context, envelope report.Snapshot) error {
	// Accepted reports are durable desired state. A bounded background worker
	// reconciles DNS and Service approvals; external outages must not consume the
	// heartbeat lease or queue callers behind cleanup/provider operations.
	c.reportMu.Lock()
	defer c.reportMu.Unlock()
	if err := ctx.Err(); err != nil {
		code := connect.CodeCanceled
		if errors.Is(err, context.DeadlineExceeded) {
			code = connect.CodeDeadlineExceeded
		}
		return rpcFailure(code, err)
	}
	if err := c.policyStorageError(); err != nil {
		return rpcFailure(connectCodeHTTP(503), errors.New("Exposure policy unavailable; retry later"))
	}
	if err := envelope.Validate(time.Now(), 5*time.Minute); err != nil {
		return rpcFailure(connectCodeHTTP(http.StatusUnauthorized), errors.New(err.Error()))
	}
	if reportHasRemovedEdgeField(envelope.Payload) {
		return rpcFailure(connectCodeHTTP(400), errors.New("loadbalancer_id is not accepted in runtime reports"))
	}
	p := rpcPrincipal(ctx)
	node, nodeErr := c.Store.Node(strings.TrimSpace(envelope.NodeID))
	if nodeErr == nil && node.Mode == "loadbalancer" {
		return c.acceptDirectReport(ctx, envelope, node)
	}
	if p.NodeID == "" || nodeErr != nil || !store.IsGateway(node.Mode) || node.TailnetID == "" || node.TailnetID != p.NodeID {
		return rpcFailure(connectCodeHTTP(403), errors.New(fmt.Sprintf("report node does not match registered tailnet identity (report=%s whois=%s stored=%s err=%v)", envelope.NodeID, p.NodeID, node.TailnetID, nodeErr)))
	}
	if node.ParentID != "" {
		parent, e := c.Store.Node(node.ParentID)
		if e != nil || parent.Revoked {
			return rpcFailure(connectCodeHTTP(403), errors.New("controller revoked"))
		}
	}
	revoked, err := c.Store.NodeRevoked(strings.TrimSpace(envelope.NodeID))
	if err != nil {
		return rpcFailure(connectCodeHTTP(http.StatusInternalServerError), errors.New(err.Error()))
	}
	if revoked {
		return rpcFailure(connectCodeHTTP(http.StatusForbidden), errors.New("unknown or revoked node"))
	}
	var freshness struct {
		ExpiresAt int64 `json:"expires_at"`
	}
	if err := json.Unmarshal(envelope.Payload, &freshness); err != nil || freshness.ExpiresAt <= time.Now().Unix() || freshness.ExpiresAt > envelope.Timestamp+int64(5*time.Minute/time.Second) {
		return rpcFailure(connectCodeHTTP(400), errors.New("invalid report expiry"))
	}
	var reportPayload struct {
		ProviderID string                `json:"provider_id"`
		Exposures  []dns.GatewayExposure `json:"exposures"`
	}
	if err := json.Unmarshal(envelope.Payload, &reportPayload); err != nil {
		return rpcFailure(connectCodeHTTP(http.StatusBadRequest), errors.New("invalid report payload"))
	}
	gatewayIDs := []string{node.ID, node.ParentID}
	roles := p.RoleSelectors()
	applications, applicationsErr := c.Store.Applications()
	if applicationsErr != nil {
		return rpcFailure(connectCodeHTTP(http.StatusInternalServerError), errors.New(applicationsErr.Error()))
	}
	if node.Mode == "docker-gateway" {
		claims := map[string]string{}
		for _, e := range reportPayload.Exposures {
			key := e.PublicHostname + ":" + gateway.NormalizePrefix(e.PathPrefix)
			if _, ok := claims[key]; ok {
				return rpcFailure(connect.CodeInvalidArgument, errors.New("duplicate Docker hostname/path claim"))
			}
			claims[key] = e.ContainerID
		}
	}
	reportPayload.Exposures = c.expandDockerAliases(reportPayload.Exposures, applications, node, roles)
	// Keep the authenticated snapshot for audit; derive only current backend
	// assignments from it. A known retired claim cannot starve other apps.
	filtered := reportPayload.Exposures[:0]
	for _, exposure := range reportPayload.Exposures {
		if node.Mode == "docker-gateway" {
			app, found := approvedApplication(applications, exposure)
			valid := found && applicationAllows(app, node) && contains(app.PublicHostnames, exposure.PublicHostname)
			retired, e := c.Store.RetiredDockerExposure(node.ID, exposure.ApplicationID, exposure.PublicHostname, exposure.ServiceName, exposure.Port)
			if e != nil {
				return rpcFailure(connect.CodeUnavailable, e)
			}
			if !valid && retired {
				continue
			}
			if valid {
				ok, e := c.Store.DockerDeclarationAllowed(node.ID, app, exposure.ContainerID, true)
				if e != nil {
					return rpcFailure(connect.CodeUnavailable, e)
				}
				if !ok {
					continue
				}
			}
		}
		filtered = append(filtered, exposure)
	}
	reportPayload.Exposures = filtered
	for i := range reportPayload.Exposures {
		exposure := &reportPayload.Exposures[i]
		application, applicationOK := approvedApplication(applications, *exposure)
		if !applicationOK || !applicationAllows(application, node) || !dockerReportRouteMatches(application, *exposure) {
			return rpcFailure(connectCodeHTTP(http.StatusForbidden), errors.New("exposure does not match an enabled application Service"))
		}
		if store.BackendSource(node.Mode) != application.BackendSource && !(node.Mode == "docker-gateway" && application.BackendSource == "") {
			return rpcFailure(connectCodeHTTP(403), errors.New("gateway backend source mismatch"))
		}
		if !exposure.Reachable {
			continue
		}
		exposure.ApplicationID = application.ID
		exposure.ServiceName = application.ServiceName
		if !c.applicationAnyEdgeAllowed(application, gatewayIDs, roles) {
			return rpcFailure(connectCodeHTTP(403), errors.New("gateway exposure is not allowed by the control policy"))
		}
		if _, err := c.managedZone(exposure.PublicHostname); err != nil {
			return rpcFailure(connectCodeHTTP(400), errors.New(err.Error()))
		}

	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return rpcFailure(connectCodeHTTP(http.StatusBadRequest), errors.New(err.Error()))
	}
	if err := c.Store.AcceptReport(strings.TrimSpace(envelope.NodeID), envelope.Nonce, payload); err != nil {
		if strings.Contains(err.Error(), "nonce already used") {
			return rpcFailure(connectCodeHTTP(http.StatusConflict), errors.New(err.Error()))
		}
		return rpcFailure(connectCodeHTTP(http.StatusInternalServerError), errors.New(err.Error()))
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, roles)
	activeBackends := map[string]bool{}
	for _, exposure := range reportPayload.Exposures {
		activeBackends[backendRecordID(envelope.NodeID, exposure)] = true
		if err := c.Store.UpsertRegisteredBackend(store.BackendAssignment{
			ID: backendRecordID(envelope.NodeID, exposure), ApplicationID: exposure.ApplicationID, NodeID: envelope.NodeID,
			ContainerID: exposure.ContainerID, BackendIP: exposure.BackendIP, BackendPort: exposure.Port,
			Networks: exposure.Networks, Reachable: exposure.Reachable, ConfigRevision: envelope.Timestamp,
			LastSeen: time.Now().Unix(),
		}); err != nil {
			return rpcFailure(connectCodeHTTP(http.StatusInternalServerError), errors.New(err.Error()))
		}
	}
	if err := c.Store.ReconcileBackends(envelope.NodeID, activeBackends); err != nil {
		return rpcFailure(connectCodeHTTP(http.StatusInternalServerError), errors.New(err.Error()))
	}
	return nil
}

func (c *Control) acceptPeerSnapshot(ctx context.Context, env report.Snapshot) error {
	node, err := c.callerNodeContext(ctx, "")
	if err != nil {
		return rpcFailure(connectCodeHTTP(403), errors.New("Unknown or revoked node"))
	}
	if node.Mode != "tailnet-gateway" {
		return rpcFailure(connectCodeHTTP(403), errors.New("Tailnet gateway required"))
	}
	if env.NodeID != node.ID {
		return rpcFailure(connectCodeHTTP(403), errors.New("Snapshot identity mismatch"))
	}
	if err = env.Validate(time.Now(), 5*time.Minute); err != nil {
		return rpcFailure(connectCodeHTTP(401), errors.New("Invalid snapshot signature or timestamp"))
	}
	var snapshot nodeconfig.PeerSnapshot
	if json.Unmarshal(env.Payload, &snapshot) != nil || len(snapshot.Peers) > 2000 || len(snapshot.Results) > 20 || snapshot.ExpiresAt <= time.Now().Unix() || snapshot.ExpiresAt > env.Timestamp+300 {
		return rpcFailure(connectCodeHTTP(400), errors.New("Invalid peer snapshot"))
	}
	for _, source := range snapshot.Sources {
		if source != "docker" && source != "tailnet" {
			return rpcFailure(connectCodeHTTP(400), errors.New("Invalid backend source"))
		}
	}
	seen := map[string]bool{}
	for _, p := range snapshot.Peers {
		if p.ID == "" || seen[p.ID] || len(p.Addresses) == 0 || len(p.Addresses) > 8 {
			return rpcFailure(connectCodeHTTP(400), errors.New("Invalid or duplicate peer"))
		}
		seen[p.ID] = true
		for _, address := range p.Addresses {
			ip, e := netip.ParseAddr(address)
			if e != nil || !tailnetAddress(ip) {
				return rpcFailure(connectCodeHTTP(400), errors.New("Peer addresses must be Tailscale addresses"))
			}
		}
	}
	snapshot.Tags = append([]string{}, rpcPrincipal(ctx).Tags...)
	snapshot.Roles = rpcPrincipal(ctx).RoleSelectors()
	snapshot.ReceivedAt = time.Now().Unix()
	var probes []nodeconfig.Probe
	_ = c.Store.GetJSON("node_probes:"+node.ID, &probes)
	for _, result := range snapshot.Results {
		found := false
		for _, probe := range probes {
			if result.ID == probe.ID && result.DeviceID == probe.DeviceID && result.Port == probe.Port && probe.ExpiresAt > time.Now().Unix() && result.CheckedAt <= time.Now().Unix()+30 && result.CheckedAt >= env.Timestamp-30 {
				found = true
			}
		}
		if !found {
			return rpcFailure(connectCodeHTTP(400), errors.New("Unknown or expired probe result"))
		}
	}
	// Retain verified completed probes while an ordinary peer refresh is in flight.
	// Refreshes do not invalidate an administrator's still-fresh connection test.
	if len(snapshot.Results) == 0 {
		var previous nodeconfig.PeerSnapshot
		_ = c.Store.GetJSON("node_peers:"+node.ID, &previous)
		for _, result := range previous.Results {
			for _, probe := range probes {
				if result.ID == probe.ID && probe.ExpiresAt > time.Now().Unix() && result.CheckedAt > time.Now().Add(-2*time.Minute).Unix() {
					snapshot.Results = append(snapshot.Results, result)
				}
			}
		}
	}
	payload, _ := json.Marshal(snapshot)
	if err = c.Store.AcceptNodeSnapshot(node.ID, env.Nonce, "node_peers:"+node.ID, payload); err != nil {
		return rpcFailure(connectCodeHTTP(409), errors.New("Snapshot rejected: replay or revoked identity"))
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, rpcPrincipal(ctx).RoleSelectors())
	return nil
}

func (c *Control) resolveApplication(ctx context.Context, request *v1.ResolveApplicationRequest) (report.Snapshot, error) {
	hostname, backendPort := request.Hostname, uint16(request.BackendPort)
	node, err := c.callerNodeContext(ctx, "docker-gateway")
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New(err.Error()))
	}
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if err := policy.ValidateHostname(hostname); err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(400), errors.New(err.Error()))
	}
	var path string
	var rewrite *proxy.PrefixRewrite
	if request != nil {
		path = request.PathPrefix
		if request.Rewrite != nil {
			rewrite = &proxy.PrefixRewrite{ReplacePrefix: request.Rewrite.ReplacePrefix}
		}
	}
	if path != "" {
		if e := proxy.ValidatePath(path); e != nil {
			return report.Snapshot{}, rpcFailure(connect.CodeInvalidArgument, e)
		}
		path = gateway.NormalizePrefix(path)
	}
	if rewrite != nil && path == "" {
		return report.Snapshot{}, rpcFailure(connect.CodeInvalidArgument, errors.New("rewrite requires a path prefix"))
	}
	if rewrite != nil {
		if e := proxy.ValidatePath(rewrite.ReplacePrefix); e != nil {
			return report.Snapshot{}, rpcFailure(connect.CodeInvalidArgument, e)
		}
	}
	var settings struct {
		Name         string `json:"name"`
		LoadBalancer string `json:"loadbalancer_hostname"`
	}
	if err := c.Store.GetJSON("tailnet", &settings); err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(503), errors.New("Unable to read control settings"))
	}
	if allowed, _ := c.loadExposurePolicy().Allows(hostname, []string{node.ID, node.TailnetID}, rpcPrincipal(ctx).RoleSelectors()); !allowed {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New("hostname denied by exposure policy"))
	}
	if _, err := c.managedZone(hostname); err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New(err.Error()))
	}
	declaration := store.ApplicationRecord{BackendPort: backendPort, PublicHostnames: []string{hostname}}
	if path != "" {
		declaration.HTTPRules = []proxy.HTTPRule{{Matches: []proxy.Match{{Path: path}}, Rewrite: rewrite}}
	}
	if ok, e := c.Store.DockerDeclarationAllowed(node.ID, declaration, "", true); e != nil {
		return report.Snapshot{}, rpcFailure(connect.CodeUnavailable, e)
	} else if !ok {
		return report.Snapshot{}, rpcFailure(connect.CodeFailedPrecondition, errors.New("declaration absent from current Docker inventory"))
	}
	// Discovery re-resolves existing labels on every tick. Reading an unchanged,
	// authorized binding must never wait behind creation of another Service.
	current, err := c.Store.Applications()
	if err != nil {
		return report.Snapshot{}, rpcFailure(connect.CodeUnavailable, err)
	}
	for _, app := range current {
		if app.BackendSource != "docker" && app.BackendSource != "" {
			continue
		}
		if contains(app.PublicHostnames, hostname) && applicationAllows(app, node) && app.BackendPort == backendPort && app.ServiceFQDN != "" && dockerReportRouteMatches(app, dns.GatewayExposure{PathPrefix: path, Rewrite: rewrite}) {
			return c.nodeConfiguration(ctx)
		}
	}
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return report.Snapshot{}, rpcFailure(connect.CodeDeadlineExceeded, err)
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}
	var existing *store.ApplicationRecord
	for _, app := range apps {
		if !contains(app.PublicHostnames, hostname) {
			continue
		}
		if app.BackendSource == "kubernetes" || app.BackendSource == "tailnet" || !applicationAllows(app, node) {
			return report.Snapshot{}, rpcFailure(connect.CodeAlreadyExists, errors.New("hostname already assigned to another backend source"))
		}
		appPath := "/"
		if len(app.HTTPRules) > 0 && len(app.HTTPRules[0].Matches) > 0 {
			appPath = gateway.NormalizePrefix(app.HTTPRules[0].Matches[0].Path)
		}
		if appPath != gateway.NormalizePrefix(path) {
			continue
		}
		if !applicationAllows(app, node) || app.BackendPort != backendPort {
			return report.Snapshot{}, rpcFailure(connect.CodeAlreadyExists, errors.New("hostname/path already assigned to a different gateway or backend port"))
		}
		copy := app
		existing = &copy
	}
	idInput := node.ID + ":" + hostname + fmt.Sprintf(":%d", backendPort)
	if path != "" {
		idInput += ":" + path
	}
	id := fmt.Sprintf("auto-%x", sha256.Sum256([]byte(idInput)))[:25]
	app := store.ApplicationRecord{ID: id, ServiceName: "svc:tailscale-private-funnel-app-" + id, ServicePort: backendPort, BackendPort: backendPort, PublicHostnames: []string{hostname}, AllowedGatewayNodes: []string{node.ID}, Enabled: true}
	if existing != nil {
		app = *existing
	}
	app.HTTPRules = nil
	if path != "" {
		app.HTTPRules = []proxy.HTTPRule{{Matches: []proxy.Match{{Path: path}}, Rewrite: rewrite, Backends: []proxy.Backend{{Key: app.ID, Service: app.ServiceFQDN, Port: app.ServicePort, Weight: 1}}}}
	}
	if existing != nil {
		if nodeconfig.Revision(existing.HTTPRules) != nodeconfig.Revision(app.HTTPRules) {
			if err := c.Store.UpsertApplication(app); err != nil {
				return report.Snapshot{}, rpcFailure(connect.CodeInternal, err)
			}
			_ = c.Store.Audit("application.route.update:"+app.ID, "node:"+node.TailnetID)
		}
		return c.nodeConfiguration(ctx)
	}
	clientID, err := c.Store.GetSecret("tailscale_services_client_id")
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(428), errors.New("configure Tailscale OAuth first"))
	}
	secret, err := c.Store.GetSecret("tailscale_services_client_secret")
	if err != nil || settings.Name == "" {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(428), errors.New("configure Tailscale OAuth and tailnet first"))
	}
	service, err := c.tsClient(clientID, secret, settings.Name).CreateService(ctx, tailscale.Service{Name: app.ServiceName, DisplayName: hostname, Ports: []string{fmt.Sprintf("tcp:%d", app.ServicePort)}, Tags: []string{identity.ServiceTag(c.installation(), false)}})
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(502), errors.New(err.Error()))
	}
	app.ServiceFQDN = selectServiceTarget(service.Addresses, app.ServiceName, settings.Name)
	for i := range app.HTTPRules {
		for j := range app.HTTPRules[i].Backends {
			app.HTTPRules[i].Backends[j].Service = app.ServiceFQDN
		}
	}
	if err := c.Store.UpsertApplication(app); err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}
	_ = c.Store.Audit("application.autoprovision:"+hostname, "node:"+node.TailnetID)
	return c.nodeConfiguration(ctx)
}

func (c *Control) acceptKubernetesSnapshot(ctx context.Context, env report.Snapshot) (report.Snapshot, error) {
	node, err := c.callerNodeContext(ctx, "k8s-gateway")
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New("Kubernetes gateway required"))
	}
	if env.NodeID != node.ID {
		return report.Snapshot{}, rpcFailure(connect.CodePermissionDenied, errors.New("authenticated gateway snapshot required"))
	}
	if err = env.Validate(time.Now(), 5*time.Minute); err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(401), errors.New(err.Error()))
	}
	var snapshot nodeconfig.KubernetesSnapshot
	payloadDecoder := json.NewDecoder(bytes.NewReader(env.Payload))
	payloadDecoder.DisallowUnknownFields()
	if payloadDecoder.Decode(&snapshot) != nil || snapshot.ClusterID == "" || len(snapshot.ClusterID) > 128 || len(snapshot.Hosts) > 500 || snapshot.ExpiresAt <= time.Now().Unix() || snapshot.ExpiresAt > env.Timestamp+300 {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(400), errors.New("invalid Kubernetes snapshot"))
	}
	seen := map[string]bool{}
	for _, host := range snapshot.Hosts {
		if policy.ValidateHostname(host.Hostname) != nil || seen[host.Hostname] || len(host.Rules) == 0 || len(host.Rules) > 100 {
			return report.Snapshot{}, rpcFailure(connectCodeHTTP(400), errors.New("invalid or duplicate hostname"))
		}
		seen[host.Hostname] = true
		if ok, _ := c.loadExposurePolicy().Allows(host.Hostname, []string{node.ID, node.TailnetID}, rpcPrincipal(ctx).RoleSelectors()); !ok {
			return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New("hostname denied by exposure policy"))
		}
		if _, err := c.managedZone(host.Hostname); err != nil {
			return report.Snapshot{}, rpcFailure(connectCodeHTTP(403), errors.New(err.Error()))
		}
		for _, rule := range host.Rules {
			if err := proxy.ValidateHTTPRule(rule, false); err != nil {
				return report.Snapshot{}, rpcFailure(connectCodeHTTP(400), errors.New(err.Error()))
			}
		}
	}
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, err := c.Store.Applications()
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
	}
	for _, h := range snapshot.Hosts {
		for _, a := range apps {
			for _, name := range a.PublicHostnames {
				if name == h.Hostname && (a.BackendSource != "kubernetes" || !contains(a.AllowedGatewayNodes, node.ID)) {
					return report.Snapshot{}, rpcFailure(connectCodeHTTP(409), errors.New("hostname owned by another gateway or source"))
				}
			}
		}
	}
	// Consume the nonce before external mutations; retry uses a fresh envelope.
	if err = c.Store.AcceptNodeSnapshot(node.ID, env.Nonce, "node_kubernetes:"+node.ID, env.Payload); err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(409), errors.New("snapshot rejected or replayed"))
	}
	ts, err := c.tailscaleRuntimeClient()
	if err != nil {
		return report.Snapshot{}, rpcFailure(connectCodeHTTP(428), errors.New(err.Error()))
	}
	desired := map[string]bool{}
	for _, h := range snapshot.Hosts {
		id := fmt.Sprintf("k8s-%x", sha256.Sum256([]byte(node.ID+":"+snapshot.ClusterID+":"+h.Hostname)))[:28]
		desired[id] = true
		backends := map[string]proxy.Backend{}
		for ri := range h.Rules {
			for bi, b := range h.Rules[ri].Backends {
				assigned, ok := backends[b.Key]
				if !ok {
					assigned = b
					assigned.Service = "" // Only control-owned bindings may supply a tailnet target.
					assigned.ServiceName = fmt.Sprintf("svc:tpf-k8s-%x", sha256.Sum256([]byte(node.ID+":"+snapshot.ClusterID+":"+b.Key)))[:40]
					// Service identity and port are immutable for this backend key.
					// Routine snapshots reuse the binding, avoiding a REST mutation
					// for every healthy backend on every watch refresh.
					for _, existing := range apps {
						if existing.BackendSource != "kubernetes" || !contains(existing.AllowedGatewayNodes, node.ID) {
							continue
						}
						for _, cached := range existing.HTTPBackends {
							if cached.ServiceName == assigned.ServiceName && cached.Key == b.Key && cached.Port == b.Port && cached.Service != "" {
								assigned.Service = cached.Service
							}
						}
					}
					if assigned.Service == "" {
						service, e := ts.CreateService(ctx, tailscale.Service{Name: assigned.ServiceName, DisplayName: h.Hostname, Ports: []string{fmt.Sprintf("tcp:%d", b.Port)}, Tags: []string{identity.ServiceTag(c.installation(), false)}})
						if e != nil {
							return report.Snapshot{}, rpcFailure(connectCodeHTTP(502), errors.New(e.Error()))
						}
						assigned.Service = selectServiceTarget(service.Addresses, assigned.ServiceName, ts.Tailnet)
					}
					backends[b.Key] = assigned
				}
				assigned.Weight = b.Weight
				h.Rules[ri].Backends[bi] = assigned
			}
		}
		keys := []string{}
		for key := range backends {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		all := []proxy.Backend{}
		for _, key := range keys {
			all = append(all, backends[key])
		}
		a := store.ApplicationRecord{ID: id, Name: h.Hostname, BackendSource: "kubernetes", Enabled: true, PublicHostnames: []string{h.Hostname}, AllowedGatewayNodes: []string{node.ID}, HTTPRules: h.Rules, HTTPBackends: all, PolicyRevision: int64(c.loadExposurePolicy().Revision), ServiceName: "svc:tpf-redirect-" + id, ServiceFQDN: "redirect.invalid", ServicePort: 80, BackendPort: 80}
		if len(all) > 0 {
			a.ServiceName = all[0].ServiceName
			a.ServiceFQDN = all[0].Service
			a.ServicePort = all[0].Port
			a.BackendPort = all[0].Port
		}
		if err = c.Store.UpsertApplication(a); err != nil {
			return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
		}
	}
	for _, a := range apps {
		if a.BackendSource == "kubernetes" && applicationAllows(a, node) && !desired[a.ID] {
			a.Enabled = false
			if err = c.Store.UpsertApplication(a); err != nil {
				return report.Snapshot{}, rpcFailure(connectCodeHTTP(500), errors.New(err.Error()))
			}
		}
	}
	_ = c.Store.Audit("kubernetes.desired:"+node.ID, "node:"+node.TailnetID)
	return c.nodeConfiguration(ctx)
}

func (c *Control) acceptPublicEndpoint(ctx context.Context, env report.Snapshot) error {
	n, err := c.callerNodeContext(ctx, "loadbalancer")
	if err != nil {
		return rpcFailure(connectCodeHTTP(403), errors.New("registered load balancer required"))
	}
	d, err := c.nodeDeployment(n.ID)
	if err != nil || d.AddressSource != "kubernetes" {
		return rpcFailure(connectCodeHTTP(403), errors.New("Kubernetes address discovery not authorized for this node"))
	}
	if env.NodeID != n.ID {
		return rpcFailure(connect.CodePermissionDenied, errors.New("authenticated endpoint required"))
	}
	if err = env.Validate(time.Now(), time.Minute); err != nil {
		return rpcFailure(connectCodeHTTP(401), errors.New("invalid endpoint signature or timestamp"))
	}
	var in publicEndpoint
	dec := json.NewDecoder(strings.NewReader(string(env.Payload)))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.Namespace != d.KubernetesNamespace || in.Service != d.PublicService || in.UID == "" || len(in.UID) > 128 || len(in.Ingress) > 16 || in.ObservedAt < time.Now().Add(-time.Minute).Unix() || in.ObservedAt > time.Now().Add(time.Minute).Unix() {
		return rpcFailure(connectCodeHTTP(400), errors.New("invalid or mismatched public Service observation"))
	}
	// Binding and nonce consumption are serialized so the first Service UID cannot race.
	c.endpointMu.Lock()
	defer c.endpointMu.Unlock()
	var previous publicEndpoint
	err = c.Store.GetJSON("node_public_endpoint:"+n.ID, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return rpcFailure(connectCodeHTTP(503), errors.New("Service binding unavailable"))
	}
	if err == nil && previous.UID != in.UID {
		return rpcFailure(connectCodeHTTP(409), errors.New("Service identity changed; recreate the component after checking its public Service binding"))
	}
	if err = c.Store.AcceptNodeSnapshot(n.ID, env.Nonce, "node_public_endpoint:"+n.ID, env.Payload); err != nil {
		return rpcFailure(connectCodeHTTP(409), errors.New("endpoint replayed or unavailable"))
	}
	_ = c.Store.Audit("loadbalancer.public-endpoint:"+n.ID, "node:"+n.TailnetID)
	err = c.reconcileLoadBalancerNode(ctx, n)

	return nil
}

// Revoked nodes retain only caller-scoped cleanup authority while their device still exists.

func dockerReportRouteMatches(app store.ApplicationRecord, e dns.GatewayExposure) bool {
	if app.BackendSource != "docker" && app.BackendSource != "" {
		return true
	}
	if len(app.HTTPRules) == 0 {
		return e.PathPrefix == "" && e.Rewrite == nil
	}
	if len(app.HTTPRules) != 1 || len(app.HTTPRules[0].Matches) != 1 {
		return false
	}
	rule := app.HTTPRules[0]
	return gateway.NormalizePrefix(rule.Matches[0].Path) == gateway.NormalizePrefix(e.PathPrefix) && e.PathPrefix != "" && nodeconfig.Revision(rule.Rewrite) == nodeconfig.Revision(e.Rewrite)
}
