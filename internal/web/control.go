package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

type Control struct {
	Installation      string
	PublicLookup      func(context.Context, string) ([]net.IPAddr, error)
	BuildVersion      string
	Store             *store.Store
	TailscaleClient   *tailscale.Client
	ReleaseCheck      func(context.Context, string) error
	tokenClientsMu    sync.Mutex
	tokenClients      map[[32]byte]tailscale.Client
	endpointMu        sync.Mutex
	settingsMu        sync.Mutex
	policyMu          sync.Mutex
	cleanupMu         sync.Mutex
	reportMu          sync.Mutex // Local report/state writes only; never external I/O.
	shutdownMu        sync.Mutex
	serviceApprovalMu sync.Mutex
	syncMu            contextMutex
	exposureCursor    atomic.Uint64
	accessMu          sync.Mutex
	provisionMu       sync.Mutex
	roleMu            sync.Mutex
	roleUsers         []tailscale.User
	rolesExpire       time.Time
	ReportTTL         time.Duration
}

type Principal struct {
	UserID       string
	TailnetRole  string
	NodeID       string
	Hostname     string
	Login        string
	Addresses    []string
	Groups       []string
	Tags         []string
	Tagged       bool
	Capabilities map[string][]json.RawMessage
}
type principalKey struct{}

func WithPrincipal(r *http.Request, p Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
}
func principal(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey{}).(Principal)
	return p
}

type identityContextKey struct{}

func WithIdentity(r *http.Request, identity string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity))
}

// RoleSelectors exposes the stable Tailscale identity values that can be used
// by both UI authorization and gateway exposure policy. Groups are supplied by
// WhoIs, while tags are supplied by the node identity.
func (p Principal) RoleSelectors() []string {
	seen := map[string]bool{}
	var out []string
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	if !p.Tagged && len(p.Tags) == 0 {
		if p.TailnetRole != "" {
			add("autogroup:" + p.TailnetRole)
		}
		add(p.Login)
		if p.Login != "" {
			add("user:" + p.Login)
		}
		for _, group := range p.Groups {
			add(group)
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(group)), "group:") {
				add("group:" + group)
			}
		}
	}
	if p.NodeID != "" {
		add("node:" + p.NodeID)
	}
	for _, tag := range p.Tags {
		add(tag)
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(tag)), "tag:") {
			add("tag:" + tag)
		}
	}
	return out
}

func selectorMatches(p Principal, selector string) bool {
	if strings.HasPrefix(strings.TrimSpace(selector), "node:") {
		return p.NodeID != "" && strings.TrimPrefix(strings.TrimSpace(selector), "node:") == p.NodeID
	}
	selector = strings.ToLower(strings.TrimSpace(selector))
	if selector == "autogroup:owner" || selector == "autogroup:admin" {
		return !p.Tagged && len(p.Tags) == 0 && selector == "autogroup:"+p.TailnetRole
	}
	if selector == "*" {
		return true
	}
	for _, value := range p.RoleSelectors() {
		if selector == value {
			return true
		}
	}
	return false
}

func hasRole(p Principal, roles map[string][]string, role string) bool {
	for _, selector := range roles[strings.ToLower(role)] {
		if selectorMatches(p, selector) {
			return true
		}
	}
	return false
}

func exactAdmin(p Principal, admins []string) bool {
	for _, admin := range admins {
		admin = strings.TrimSpace(admin)
		if admin != "" && (admin == p.NodeID || selectorMatches(p, admin)) {
			return true
		}
	}
	return false
}

func (c *Control) Handler() http.Handler { return c.controlHandler() }

type syncRecord struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"`
	TTL  int    `json:"ttl"`
}
type syncInput struct {
	ProviderID    string       `json:"provider_id"`
	ZoneID        string       `json:"zone_id"`
	Zone          string       `json:"-"`
	NodeID        string       `json:"node_id"`
	Records       []syncRecord `json:"records"`
	DryRun        bool         `json:"dry_run"`
	AllowUnowned  bool         `json:"allow_unowned"`
	ApprovalToken string       `json:"approval_token"`
}

func (c *Control) reconcile(ctx context.Context, providerID, zone, ownerID string, desired []libdns.Record, dryRun, allowUnowned bool, approvalToken ...string) (result map[string]any, failure error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := c.Store.WithContext(ctx)
	defer func() {
		if dryRun {
			return
		}
		status := "Reconciled"
		if dryRun {
			status = "Preview only"
		}
		if failure != nil {
			status = "Pending / failed: " + failure.Error()
		}
		statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = c.Store.WithContext(statusCtx).SetJSON("zone_sync:"+providerID+":"+zone, status)
	}()
	if err := c.syncMu.Lock(ctx); err != nil {
		return nil, err
	}
	defer c.syncMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, config, err := s.ProviderConfig(providerID)
	if err != nil {
		return nil, fmt.Errorf("provider not found")
	}
	if !p.Enabled && len(desired) > 0 {
		return nil, fmt.Errorf("provider is disabled")
	}
	factory := dns.Registry[p.Provider]
	if factory == nil {
		return nil, fmt.Errorf("provider has no linked runtime adapter")
	}
	provider, err := factory(config)
	if err != nil {
		return nil, err
	}
	capabilities := make(map[string]bool)
	for _, capability := range provider.Capabilities() {
		capabilities[strings.ToUpper(capability)] = true
	}
	for _, record := range desired {
		if !capabilities[strings.ToUpper(record.RR().Type)] {
			return nil, fmt.Errorf("provider does not support %s records", record.RR().Type)
		}
	}
	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("owner ID is required")
	}
	zone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
	if zone == "" {
		return nil, fmt.Errorf("an explicit managed zone is required")
	}
	zones, err := s.Zones()
	if err != nil {
		return nil, err
	}
	managed := false
	for _, z := range zones {
		if z.ProviderID == providerID && z.Zone == zone {
			managed = true
		}
	}
	if !managed {
		return nil, fmt.Errorf("managed zone not found")
	}
	desired = dns.NormalizeRecords(desired, zone)
	for _, record := range desired {
		name := strings.ToLower(strings.TrimSuffix(record.RR().Name, "."))
		if name != zone && !strings.HasSuffix(name, "."+zone) {
			return nil, fmt.Errorf("record %s is outside zone %s", name, zone)
		}
	}
	allLedger, err := s.PartitionZoneLedger(providerID, zone)
	if err != nil {
		return nil, err
	}
	existing, err := provider.List(ctx, zone)
	if err != nil {
		return nil, fmt.Errorf("list provider records: %w", err)
	}
	existing = dns.NormalizeRecords(existing, zone)
	add, remove, err := dns.OwnershipPlan(ownerID, allLedger, existing, desired, false)
	requiresApproval := err != nil
	if requiresApproval && dryRun {
		add, remove, err = dns.OwnershipPlan(ownerID, allLedger, existing, desired, true)
		if err != nil {
			return nil, err
		}
		token, tokenErr := c.approval(providerID+":"+zone, ownerID, desired, existing, allLedger, "")
		if tokenErr != nil {
			return nil, tokenErr
		}
		return map[string]any{"add": len(add), "remove": len(remove), "requires_approval": true, "approval_token": token, "existing_records": managedRecordViews(existing, allLedger, ownerID), "desired_records": recordViews(desired)}, nil
	}
	if allowUnowned && !dryRun {
		token := ""
		if len(approvalToken) > 0 {
			token = approvalToken[0]
		}
		if token == "" {
			return nil, fmt.Errorf("overwrite approval is required; preview the specific changes first")
		}
		if _, approvalErr := c.approval(providerID+":"+zone, ownerID, desired, existing, allLedger, token); approvalErr != nil {
			return nil, approvalErr
		}
		add, remove, err = dns.OwnershipPlan(ownerID, allLedger, existing, desired, true)
	}
	if err != nil {
		return nil, fmt.Errorf("record ownership conflict: managed by another node; preview and approve the specific overwrite")
	}
	// Proxy mode is control-owned and read under the DNS mutation lock; gateway
	// snapshots and temporary HTTPRoute membership never supply this setting.
	var proxyProvider dns.ProxyProvider
	var proxyChanges []dns.ProxyChange
	proxyEnabled := false
	if strings.HasPrefix(ownerID, "exposure:") && len(desired) > 0 {
		exposure, e := s.Exposure(strings.TrimPrefix(ownerID, "exposure:"))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		proxyEnabled = exposure.CloudflareProxied
		if p.Provider == "cloudflare" {
			proxyProvider, _ = provider.(dns.ProxyProvider)
			if proxyProvider == nil {
				return nil, fmt.Errorf("Cloudflare adapter cannot configure proxy mode")
			}
			proxyChanges, err = proxyProvider.ProxyChanges(ctx, zone, desired, proxyEnabled)
			if err != nil {
				return nil, err
			}
		} else if proxyEnabled {
			return nil, fmt.Errorf("hostname proxy mode requires Cloudflare")
		}
	}
	if !dryRun {
		// A verified no-op must not delete/reinsert the ledger and fsync an audit
		// event on every poll. Keep the provider read and exact ownership checks.
		counts := map[string]int{}
		for _, entry := range allLedger {
			if entry.OwnerID == ownerID {
				counts[dns.RecordKey(entry.Record)]++
			}
		}
		for _, record := range desired {
			counts[dns.RecordKey(record)]--
		}
		ledgerChanged := false
		for _, count := range counts {
			ledgerChanged = ledgerChanged || count != 0
		}
		if len(add) == 0 && len(remove) == 0 && len(proxyChanges) == 0 && !ledgerChanged && !allowUnowned {
			return map[string]any{"add": 0, "remove": 0, "requires_approval": false, "existing_records": managedRecordViews(existing, allLedger, ownerID), "desired_records": recordViews(desired)}, nil
		}
		if err := provider.Apply(ctx, zone, add, remove); err != nil {
			return nil, err
		}
		// External mutation succeeded. Do not abandon its ownership commit merely
		// because the provider used the remainder of the request deadline.
		commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		s = c.Store.WithContext(commitCtx)
		if allowUnowned {
			transferred := append(append(append([]libdns.Record{}, desired...), add...), remove...)
			if err := s.TransferZoneLedgerOwnership(zone, ownerID, transferred); err != nil {
				return nil, err
			}
		}
		if err := s.ReplaceLedger(p.ID, zone, ownerID, desired); err != nil {
			return nil, err
		}
		// Commit newly created RR ownership before a separate proxy update. If the
		// Cloudflare PATCH fails, the next pass can safely retry the owned record.
		if proxyProvider != nil {
			proxyChanges, err = proxyProvider.ProxyChanges(ctx, zone, desired, proxyEnabled)
			if err != nil {
				return nil, err
			}
			if err = proxyProvider.ApplyProxyChanges(ctx, proxyChanges); err != nil {
				return nil, err
			}
		}
		if assignment, err := c.managedZone(zone); err == nil && assignment.ProviderID == providerID {
			_ = s.SetJSON("zone_last_sync:"+assignment.ID, time.Now().Unix())
		}
		_ = s.Audit("dns.reconcile:"+ownerID, "control")
		if allowUnowned {
			_ = s.Audit("dns.explicit-overwrite:"+ownerID, "administrator")
		}
	}
	return map[string]any{"add": len(add), "remove": len(remove), "proxy_updates": len(proxyChanges), "requires_approval": false, "existing_records": managedRecordViews(existing, allLedger, ownerID), "desired_records": recordViews(desired)}, nil
}

type exposureInput struct {
	Cleanup       bool   `json:"cleanup"`
	ProviderID    string `json:"provider_id"`
	ZoneID        string `json:"zone_id"`
	Zone          string `json:"-"`
	NodeID        string `json:"node_id"`
	DryRun        bool   `json:"dry_run"`
	AllowUnowned  bool   `json:"allow_unowned"`
	ApprovalToken string `json:"approval_token"`
	dns.GatewayExposure
	LoadBalancer dns.LoadBalancerEndpoint `json:"loadbalancer"`
}

func applicationAllows(app store.ApplicationRecord, node store.NodeRecord) bool {
	return app.Enabled && !node.Revoked && store.IsGateway(node.Mode) && (app.BackendSource != "tailnet" || len(app.AllowedGatewayNodes) == 1) && (app.BackendSource == store.BackendSource(node.Mode) || (app.BackendSource == "" && node.Mode == "docker-gateway")) && (len(app.AllowedGatewayNodes) == 0 || contains(app.AllowedGatewayNodes, node.ID) || node.ParentID != "" && contains(app.AllowedGatewayNodes, node.ParentID))
}

func (c *Control) tailscaleRuntimeClient() (tailscale.Client, error) {
	var settings struct {
		Name string `json:"name"`
	}
	if err := c.Store.GetJSON("tailnet", &settings); err != nil || strings.TrimSpace(settings.Name) == "" {
		return tailscale.Client{}, fmt.Errorf("tailnet is not configured")
	}
	id, err := c.Store.GetSecret("tailscale_services_client_id")
	if err != nil {
		return tailscale.Client{}, err
	}
	secret, err := c.Store.GetSecret("tailscale_services_client_secret")
	if err != nil {
		return tailscale.Client{}, err
	}
	return c.tsClient(id, secret, settings.Name), nil
}

type loadBalancerHealth struct {
	ReceivedAt int64 `json:"received_at"`
}

func (c *Control) loadBalancerProvider(hostname string) (store.ManagedZone, error) {
	return c.managedZone(hostname)
}

func (c *Control) reconcileLoadBalancerNode(ctx context.Context, node store.NodeRecord) error {
	var settings struct {
		Hostname string `json:"loadbalancer_hostname"`
		Name     string `json:"name"`
	}
	if err := c.Store.GetJSON("tailnet", &settings); err != nil {
		return fmt.Errorf("load balancer settings are not configured")
	}
	deployment, deploymentErr := c.nodeDeployment(node.ID)
	if deploymentErr != nil {
		return fmt.Errorf("unable to read node deployment: %w", deploymentErr)
	}
	if deployment.LoadBalancerHostname != "" {
		settings.Hostname = deployment.LoadBalancerHostname
	}
	ownerID := "loadbalancer:" + node.ID
	provider, err := c.loadBalancerProvider(settings.Hostname)
	if err != nil {
		if cleanupErr := c.reconcilePartitions(ctx, ownerID, nil); cleanupErr != nil {
			return fmt.Errorf("%v; cleanup pending: %w", err, cleanupErr)
		}
		return err
	}
	var health loadBalancerHealth
	healthErr := c.Store.GetJSON("node_health:"+node.ID, &health)
	freshness := c.ReportTTL
	if freshness <= 0 {
		freshness = 5 * time.Minute
	}
	stale := node.Revoked || healthErr != nil || health.ReceivedAt <= 0 || time.Since(time.Unix(health.ReceivedAt, 0)) > freshness
	if node.Revoked {
		return c.reconcilePartitions(ctx, ownerID, nil)
	}
	if stale {
		return fmt.Errorf("load balancer health unavailable; retaining the previous owned A record")
	}
	if deployment.AddressSource == "kubernetes" {
		ips, err := c.kubernetesAddresses(ctx, node, deployment)
		if err != nil {
			return err
		}
		records := []libdns.Record{}
		for _, ip := range ips {
			records = append(records, libdns.RR{Name: settings.Hostname, Type: "A", Data: ip, TTL: time.Minute})
		}
		return c.reconcilePartitions(ctx, ownerID, map[store.LedgerPartition][]libdns.Record{{ZoneID: provider.ID, ProviderID: provider.ProviderID, Zone: provider.Zone, OwnerID: ownerID}: records})
	}
	client, err := c.tailscalePurposeClient("endpoints")
	if err != nil {
		return fmt.Errorf("load balancer endpoint lookup: %w; retaining the previous owned A record", err)
	}
	device, err := client.GetDevice(ctx, node.TailnetID)
	if err != nil {
		return fmt.Errorf("load balancer endpoint lookup: %w; retaining the previous owned A record", err)
	}
	endpoints := dns.PublicIPv4Endpoints(device.ClientConnectivity.Endpoints)
	// Cache only control-fetched endpoints for read-only diagnostics.
	_ = c.Store.SetJSON("node_endpoints:"+node.ID, map[string]any{"public_ipv4": endpoints, "checked_at": time.Now().Unix()})
	record, preserve, err := dns.LoadBalancerRecord(dns.LoadBalancerEndpoint{Hostname: settings.Hostname, PublicIPv4: endpoints})
	if err != nil {
		if preserve {
			return fmt.Errorf("%w; retaining the previous owned A record", err)
		}
		return err
	}
	desired := map[store.LedgerPartition][]libdns.Record{{ZoneID: provider.ID, ProviderID: provider.ProviderID, Zone: provider.Zone, OwnerID: ownerID}: {record}}
	return c.reconcilePartitions(ctx, ownerID, desired)
}

// ReconcileLoadBalancerDNS applies verified endpoints on the periodic control
// loop. Health loss and missing or ambiguous endpoints retain owned records;
// only explicit revocation authorizes removal of this load balancer's records.
func (c *Control) ReconcileLoadBalancerDNS(ctx context.Context) error {
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	nodes, err := c.Store.Nodes("loadbalancer")
	if err != nil {
		return err
	}
	for _, item := range nodes {
		id := stringValue(item, "id", "")
		if id == "" {
			continue
		}
		node, nodeErr := c.Store.Node(id)
		if nodeErr != nil {
			return nodeErr
		}
		if syncErr := c.reconcileLoadBalancerNode(ctx, node); syncErr != nil {
			// An unavailable provider or ambiguous endpoint should not prevent
			// other nodes and report expiry from being processed.
			slog.Warn("load balancer DNS reconciliation failed", "node", id, "error", syncErr)
		}
	}
	return nil
}

var (
	applicationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
	serviceNamePattern   = regexp.MustCompile(`^svc:[A-Za-z0-9][A-Za-z0-9:_-]{0,62}$`)
)

func validateServiceTarget(value string) error {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	if value == "" || strings.ContainsAny(value, " \t\r\n{}:/") {
		return fmt.Errorf("invalid service target")
	}
	if ip, err := netip.ParseAddr(value); err == nil && ip.IsValid() {
		return nil
	}
	return policy.ValidateHostname(value)
}

func selectServiceTarget(addresses []string, serviceName, tailnet string) string {
	// Prefer the MagicDNS address returned by the Services API. A TailVIP is
	// also valid, but a name keeps route diagnostics readable.
	for _, address := range addresses {
		candidate := strings.TrimSuffix(strings.TrimSpace(address), ".")
		if validateServiceTarget(candidate) == nil && !strings.Contains(candidate, ".") {
			continue
		}
		if validateServiceTarget(candidate) == nil {
			return candidate
		}
	}
	for _, address := range addresses {
		candidate := strings.TrimSuffix(strings.TrimSpace(address), ".")
		if validateServiceTarget(candidate) == nil {
			return candidate
		}
	}
	name := strings.TrimPrefix(strings.TrimSpace(serviceName), "svc:")
	tailnet = strings.TrimSuffix(strings.TrimSpace(tailnet), ".")
	if name != "" && tailnet != "" {
		candidate := name + "." + tailnet
		if validateServiceTarget(candidate) == nil {
			return candidate
		}
	}
	return ""
}

// ReconcileServiceApprovals is intentionally conservative. It only approves a
// pending host when the Service is an application created in this control
// store, the device is an registered active gateway node, and the
// application explicitly allows that node. Unknown advertisements are left
// pending for an administrator to inspect.
func (c *Control) ReconcileServiceApprovals(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Policy changes and periodic maintenance can both request reconciliation.
	// Do not queue a second pass behind external API calls with a ticking deadline.
	if !c.serviceApprovalMu.TryLock() {
		return nil
	}
	defer c.serviceApprovalMu.Unlock()
	client, err := c.tailscaleRuntimeClient()
	if err != nil {
		return nil
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return err
	}
	groups := map[string][]store.ApplicationRecord{}
	for _, app := range apps {
		if len(app.HTTPBackends) == 0 {
			groups[app.ServiceName] = append(groups[app.ServiceName], app)
		} else {
			for _, b := range app.HTTPBackends {
				copy := app
				copy.ServiceName = b.ServiceName
				groups[b.ServiceName] = append(groups[b.ServiceName], copy)
			}
		}
	}
	failures := []string{}
	for service, owners := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.HasPrefix(service, "svc:") {
			continue
		}
		hosts, e := client.ListServiceHosts(ctx, service)
		if e != nil {
			if strings.Contains(e.Error(), "404") {
				continue
			}
			failures = append(failures, e.Error())
			continue
		}
		for _, host := range hosts {
			allowed, uncertain := false, false
			for _, app := range owners {
				if !app.Enabled {
					continue
				}
				decision, lookupErr := c.serviceHostAllowed(ctx, client, app, host.NodeID)
				if lookupErr != nil {
					uncertain = true
					failures = append(failures, lookupErr.Error())
				}
				allowed = allowed || decision
			}
			approved := host.ApprovalLevel == "approved:auto" || host.ApprovalLevel == "approved:manual"
			if !allowed && uncertain {
				// An unavailable authority cannot grant a new host or revoke a
				// healthy existing host. Fresh reports/acks still bound this state.
				_ = c.Store.UpsertServiceHost(store.ServiceHostRecord{ServiceName: service, DeviceID: host.NodeID, Approved: approved, ApprovalState: "authorization-unavailable"})
				continue
			}

			state := "pending"
			if approved {
				state = "approved"
			}
			if !allowed {
				state = "rejected"
			}
			if e = c.Store.UpsertServiceHost(store.ServiceHostRecord{ServiceName: service, DeviceID: host.NodeID, Approved: approved && allowed, ApprovalState: state}); e != nil {
				return e
			}
		}
		unused := strings.HasPrefix(service, "svc:tpf-k8s-")
		for _, app := range owners {
			unused = unused && !app.Enabled && app.BackendSource == "kubernetes"
		}
		if unused && c.provisionMu.TryLock() {
			current, readErr := c.Store.Applications()
			if readErr != nil {
				c.provisionMu.Unlock()
				return readErr
			}
			for _, app := range current {
				if !app.Enabled {
					continue
				}
				for _, b := range app.HTTPBackends {
					if b.ServiceName == service {
						unused = false
					}
				}
			}
			if !unused {
				c.provisionMu.Unlock()
				continue
			}
			if e = client.DeleteService(ctx, service); e != nil && !strings.Contains(e.Error(), "404") {
				failures = append(failures, e.Error())
			}
			c.provisionMu.Unlock()
		}
	}
	if len(failures) > 0 {
		_ = c.Store.Audit("service.reconcile.pending", strings.Join(failures, "; "))
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

// Service advertisements belong to the registered gateway device itself.
func (c *Control) serviceHostAllowed(ctx context.Context, _ tailscale.Client, app store.ApplicationRecord, deviceID string) (bool, error) {
	nodes, e := c.Store.Nodes("")
	if e != nil {
		return false, e
	}
	for _, item := range nodes {
		n, e := c.Store.Node(item["id"].(string))
		if e != nil {
			return false, e
		}
		if n.TailnetID != deviceID || !applicationAllows(app, n) {
			continue
		}
		reg, e := c.Store.ComponentRegistration(n.ID)
		if e != nil {
			continue
		}
		var tags []string
		_ = c.Store.GetJSON("node_policy_roles:"+n.ID, &tags)
		if identity.Authorize(c.installation(), reg.Role(), tags) != nil {
			continue
		}
		var ack struct {
			Services []string `json:"services"`
			At       int64    `json:"at"`
		}
		if c.Store.GetJSON("node_config_ack:"+n.ID, &ack) != nil || !contains(ack.Services, app.ServiceName) {
			continue
		}
		// Acknowledged ownership survives connectivity loss. Reports describe
		// backend health; only current configuration, role and revocation
		// decide whether this Service advertisement remains authorized.
		return true, nil
	}
	return false, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(wanted)) {
			return true
		}
	}
	return false
}

func (c *Control) ExpireReports(ctx context.Context, maxAge time.Duration) (int, error) {
	before := time.Now().Add(-maxAge).Unix()
	ids, err := c.Store.StaleReportIDs(before)
	if err != nil {
		return 0, err
	}
	removed := 0
	var failures []string
	for _, id := range ids {
		expired, err := c.Store.ExpireReportBefore(id, before)
		if err != nil {
			return removed, err
		}
		if expired {
			removed++
		}
	}
	if len(failures) > 0 {
		return removed, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return removed, nil
}

// CleanupStaleNodes removes runtime instances that stopped heartbeating.
// The control node is never selected by Store.StaleNodes.
func (c *Control) CleanupStaleNodes(ctx context.Context, maxAge time.Duration) (int, error) {
	before := time.Now().Add(-maxAge).Unix()
	nodes, err := c.Store.StaleNodes(before)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, node := range nodes {
		expired, err := c.Store.ExpireNodeBefore(node.ID, before)
		if err != nil {
			return removed, err
		}
		if !expired {
			continue
		}
		_ = c.Store.SetJSON("node_offline:"+node.ID, map[string]any{"offline": true, "last_report": node.LastReport})
		removed++
	}
	// Persistent devices survive outages. Only explicit reset or revocation
	// deletes an identity. Report expiry does not remove configured routes or DNS.
	_ = c.reconcileExposures(ctx)

	return removed, nil
}

func backendRecordID(nodeID string, exposure dns.GatewayExposure) string {
	return strings.Join([]string{nodeID, exposure.ServiceName, exposure.PublicHostname, fmt.Sprintf("%d", exposure.Port)}, ":")
}

func approvedApplication(applications []store.ApplicationRecord, exposure dns.GatewayExposure) (store.ApplicationRecord, bool) {
	hostname := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(exposure.PublicHostname), "."))
	for _, application := range applications {
		if !application.Enabled || !dockerReportRouteMatches(application, exposure) || application.ID != exposure.ApplicationID || application.ServiceName != strings.TrimSpace(exposure.ServiceName) || application.BackendPort != exposure.Port {
			continue
		}
		for _, publicHostname := range application.PublicHostnames {
			if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(publicHostname), "."), hostname) {
				return application, true
			}
		}
	}
	return store.ApplicationRecord{}, false
}
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func recordViews(records []libdns.Record) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		rr := record.RR()
		out = append(out, map[string]any{"name": rr.Name, "type": rr.Type, "data": rr.Data})
	}
	return out
}
