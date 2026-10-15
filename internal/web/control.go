package web

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"github.com/tailscale-x/tailscale-private-funnel/internal/web/ui"
)

type Control struct {
	PublicLookup    func(context.Context, string) ([]net.IPAddr, error)
	BuildVersion    string
	Store           *store.Store
	TailscaleClient *tailscale.Client
	ReleaseCheck    func(context.Context, string) error
	endpointMu      sync.Mutex
	settingsMu      sync.Mutex
	policyMu        sync.Mutex
	cleanupMu       sync.Mutex
	syncMu          sync.Mutex
	accessMu        sync.Mutex
	provisionMu     sync.Mutex
	roleMu          sync.Mutex
	roleUsers       []tailscale.User
	rolesExpire     time.Time
	ReportTTL       time.Duration
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

func identityFromRequest(r *http.Request) string {
	value, _ := r.Context().Value(identityContextKey{}).(string)
	return value
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

func requiredAccess(req *http.Request) string {
	if req.URL.Path == "/api/policies/test" {
		return "viewer"
	}
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return "viewer"
	}
	path := req.URL.Path
	if strings.HasPrefix(path, "/api/sync") || strings.HasPrefix(path, "/api/exposures/sync") || (strings.HasPrefix(path, "/api/zones/") && strings.HasSuffix(path, "/sync")) || path == "/api/reports/expire" {
		return "operator"
	}
	return "admin"
}

func (c *Control) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(c.authorize)
	assetFS, _ := fs.Sub(Assets, "assets")
	r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.FS(assetFS))))
	r.Get("/", c.dashboard)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r.Get("/settings", c.settings)
	r.Get("/kubernetes/control", c.kubernetesControlPage)
	r.Get("/api/kubernetes/control-install", c.kubernetesControlInstructions)
	r.Post("/settings", c.saveSettings)
	r.Get("/nodes/{id}/settings", c.nodeSettings)
	r.Post("/api/settings/bootstrap", c.saveBootstrap)
	r.Get("/providers", c.providers)
	r.Get("/api/providers", c.providerList)
	r.Post("/api/providers", c.providerSave)
	r.Patch("/api/providers/{id}", c.providerSave)
	r.Post("/api/providers/{id}/test", c.providerTest)
	r.Post("/api/providers/{id}/enable", c.providerEnable)
	r.Delete("/api/providers/{id}", c.providerDelete)
	r.Get("/zones", c.zones)
	r.Get("/applications", c.applications)
	r.Get("/api/providers/{id}/zones", c.providerDiscover)
	r.Get("/api/zones", c.zoneList)
	r.Post("/api/zones/{id}/test", c.zoneTest)
	r.Delete("/api/zones/{id}", c.zoneDelete)
	r.Post("/api/zones", c.zoneSave)
	r.Post("/api/sync", c.sync)
	r.Post("/api/exposures/sync", c.exposureSync)
	r.Get("/operations", c.operations)
	r.Get("/api/zones/{id}/dns", c.zoneDNSState)
	r.Post("/api/zones/{id}/sync", c.zoneDNSSync)
	r.Get("/access", c.accessPage)
	r.Get("/api/access", c.accessGet)
	r.Post("/api/access", c.accessSave)
	r.Get("/policies", c.policies)
	r.Get("/api/policies", c.policyGet)
	r.Post("/api/policies", c.policySave)
	r.Post("/api/policies/rule", c.policyRuleSave)
	r.Put("/api/policies/rules/{id}", c.policyRuleSave)
	r.Post("/api/policies/rules/{id}", c.policyRuleSave)
	r.Delete("/api/policies/rules/{id}", c.policyRuleDelete)
	r.Post("/api/policies/validate", c.policyValidate)
	r.Post("/api/policies/preview", c.policyPreview)
	r.Post("/api/policies/test", c.policyTest)
	r.Get("/api/reports", c.reports)
	r.Post("/api/reports", c.saveReport)
	r.Get("/api/node/config", c.nodeConfig)
	r.Post("/api/node/config", c.provisionApplication)
	r.Post("/api/node/config/ack", c.nodeConfigAck)
	r.Post("/api/node/health", c.nodeHealth)
	r.Post("/api/node/public-endpoint", c.publicEndpoint)
	r.Post("/api/node/kubernetes", c.kubernetesDesired)
	r.Post("/api/node/forwarders", c.enrollForwarder)
	r.Get("/api/node/forwarders", c.forwarderInventory)
	r.Post("/api/node/bind", c.bindForwarder)
	r.Post("/api/node/peers", c.nodePeers)
	r.Get("/api/nodes/{id}/peers", c.peersGet)
	r.Post("/api/nodes/{id}/probes", c.probeCreate)
	r.Get("/tailnet-exposures", c.tailnetExposurePage)
	r.Get("/api/tailnet-exposures", c.tailnetApplications)
	r.Post("/api/tailnet-exposures", c.saveTailnetExposure)
	r.Put("/api/tailnet-exposures/{id}", c.saveTailnetExposure)
	r.Delete("/api/tailnet-exposures/{id}", c.deleteTailnetExposure)
	r.Post("/api/node/shutdown", c.nodeShutdown)
	r.Get("/api/applications", c.applicationList)
	r.Post("/api/applications/{id}/aliases", c.applicationAlias)
	r.Patch("/api/nodes/{id}/name", c.renameNode)
	r.Patch("/api/enrollments/{id}/name", c.renameEnrollment)
	r.Post("/api/applications", c.applicationSave)
	r.Get("/api/services", c.serviceInventory)
	r.Post("/api/services/{service}/devices/{device}/approve", c.serviceApprove)
	r.Get("/api/audit", c.audit)
	r.Post("/api/reports/expire", c.expireReports)
	r.Post("/api/enrollments/{mode}", c.enrollmentCreate)
	r.Get("/api/enrollments/{mode}", c.enrollmentList)
	r.Post("/api/enrollments/{id}/revoke", c.enrollmentRevoke)
	r.Post("/api/enrollments/{id}/signing-key", c.enrollmentSigningKey)
	r.Get("/loadbalancers", c.nodes)
	r.Get("/gateways", c.gateways)
	r.Get("/gateways/new", c.addGateway)
	r.Get("/exposures", c.exposures)
	r.Get("/api/exposures", c.exposureMappingList)
	r.Get("/api/nodes", c.nodeList)
	r.Get("/api/nodes/{id}", c.nodeGet)
	r.Post("/api/nodes/{id}/revoke", c.nodeRevoke)
	r.Post("/api/nodes/{id}/public-service", c.bindPublicService)
	r.Post("/api/nodes/{id}/cleanup", c.nodeCleanup)
	r.Post("/api/nodes/{id}/replace", c.replaceGateway)
	r.Get("/enroll/{mode}", c.enroll)
	return r
}

// BootstrapHandler intentionally exposes only code-protected enrollment
// redemption. All management and DNS APIs remain behind the tailnet handler.
func (c *Control) BootstrapHandler() http.Handler {
	r := chi.NewRouter()
	// The installer has not joined the tailnet until after the auth-key
	// exchange. The enrollment code also protects the signing-key bind, so the
	// bootstrap listener can complete the transaction without exposing the
	// management API.
	return r
}
func (c *Control) dashboard(w http.ResponseWriter, r *http.Request) {
	providers, _ := c.Store.Providers()
	loadbalancers, _ := c.Store.Nodes("loadbalancer")
	gatewayes, _ := c.Store.Nodes("gateways")
	reports, _ := c.Store.Reports()
	audit, _ := c.Store.AuditEvents(6)
	zones, _ := c.Store.Zones()
	var tailnet struct {
		Name string `json:"name"`
	}
	_ = c.Store.GetJSON("tailnet", &tailnet)
	exposureCount := reportExposureCount(reports)
	nextTitle, nextCopy, nextHref := "Configure your first DNS provider", "Connect a provider so the control plane can reconcile owned records.", "/providers"
	if len(providers) > 0 {
		nextTitle, nextCopy, nextHref = "Create a loadbalancer", "The loadbalancer is the public edge for your tailnet services.", "/enroll/loadbalancer"
	}
	if len(loadbalancers) > 0 {
		nextTitle, nextCopy, nextHref = "Add an exposure policy", "Policies keep gateway hostnames scoped to the right node and domain.", "/policies"
	}
	if len(gatewayes) > 0 {
		nextTitle, nextCopy, nextHref = "Review gateway exposures", "Inspect the latest signed reports and publish only approved routes.", "/exposures"
	}
	loadbalancerLabel, loadbalancerTone := "No nodes", "neutral"
	if len(loadbalancers) > 0 {
		loadbalancerLabel, loadbalancerTone = "Awaiting reports", "warn"
		for _, node := range loadbalancers {
			if value, ok := node["last_report"].(int64); ok && value > 0 {
				loadbalancerLabel, loadbalancerTone = "Healthy", "ok"
				break
			}
		}
	}
	gatewayLabel, gatewayTone := "No nodes", "neutral"
	if len(gatewayes) > 0 {
		gatewayLabel, gatewayTone = "Awaiting reports", "warn"
		for _, node := range gatewayes {
			if value, ok := node["last_report"].(int64); ok && value > 0 {
				gatewayLabel, gatewayTone = "Reporting", "ok"
				break
			}
		}
	}
	providerLabel, providerTone := "No providers", "neutral"
	if len(providers) > 0 {
		providerLabel, providerTone = "Test required", "warn"
		allTested := true
		for _, provider := range providers {
			if provider.TestedAt <= 0 {
				allTested = false
				break
			}
		}
		if allTested {
			providerLabel, providerTone = "Ready", "ok"
		}
	}
	latestReport := int64(0)
	for _, node := range append(loadbalancers, gatewayes...) {
		if value, ok := node["last_report"].(int64); ok && value > latestReport {
			latestReport = value
		}
	}
	controlLabel, controlTone := "Awaiting reports", "warn"
	if latestReport > 0 {
		controlLabel, controlTone = "Operational", "ok"
	}
	var b strings.Builder
	b.WriteString(pageIntro("Control plane", "Dashboard", "A calm view of your private edge. Follow the topology from control to loadbalancer, gateway, and the services you have approved."))
	b.WriteString(`<div class="pf-stat-grid">`)
	fmt.Fprintf(&b, `<a class="pf-stat" href="/loadbalancers"><span class="pf-stat-top"><span class="pf-stat-label">Load balancers</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">public edge nodes</span></a>`, pill(loadbalancerLabel, loadbalancerTone), len(loadbalancers))
	fmt.Fprintf(&b, `<a class="pf-stat" href="/gateways"><span class="pf-stat-top"><span class="pf-stat-label">Gateways</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">reporting nodes</span></a>`, pill(gatewayLabel, gatewayTone), len(gatewayes))
	fmt.Fprintf(&b, `<a class="pf-stat" href="/exposures"><span class="pf-stat-top"><span class="pf-stat-label">Exposures</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">policy-scoped routes</span></a>`, pill("Owned", "ok"), exposureCount)
	fmt.Fprintf(&b, `<a class="pf-stat" href="/providers"><span class="pf-stat-top"><span class="pf-stat-label">DNS providers</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">%d managed zones</span></a>`, pill(providerLabel, providerTone), len(providers), len(zones))
	b.WriteString(`</div><div class="pf-dashboard-grid"><section class="pf-surface pf-topology"><div class="pf-section-heading"><div><p class="pf-kicker">Live topology</p><h2>How traffic moves</h2></div><a class="pf-text-link" href="/exposures">View exposures <span aria-hidden="true">→</span></a></div><div class="pf-topology-flow"><div class="pf-topology-node"><img src="/assets/icons/shield-check.svg" alt="" class="pf-icon"><strong>Control plane</strong><small>policy + DNS ownership</small><span class="pf-node-state">` + safeText(tailnet.Name) + `</span></div><span class="pf-topology-connector" aria-hidden="true">→</span><div class="pf-topology-node"><img src="/assets/icons/server-2.svg" alt="" class="pf-icon"><strong>Load balancers</strong><small>public TLS edge</small><span class="pf-node-state">` + fmt.Sprintf("%d nodes", len(loadbalancers)) + `</span></div><span class="pf-topology-connector" aria-hidden="true">→</span><div class="pf-topology-node"><img src="/assets/icons/route.svg" alt="" class="pf-icon"><strong>Gateway / direct peers</strong><small>Docker Services or backend machines</small><span class="pf-node-state">` + fmt.Sprintf("%d reporting", len(gatewayes)) + `</span></div><span class="pf-topology-connector" aria-hidden="true">→</span><div class="pf-topology-node"><img src="/assets/icons/world.svg" alt="" class="pf-icon"><strong>Exposures</strong><small>approved hostnames</small><span class="pf-node-state">` + fmt.Sprintf("%d active", exposureCount) + `</span></div></div></section><aside class="pf-surface pf-next-step"><p class="pf-kicker">Next step</p><h2>` + safeText(nextTitle) + `</h2><p>` + safeText(nextCopy) + `</p><a class="pf-button" href="` + safeText(nextHref) + `">Open next step <span aria-hidden="true">→</span></a><div class="pf-next-meta"><span class="pf-status-dot pf-status-ok"></span><span>Changes stay within your ownership ledger.</span></div></aside></div>`)
	b.WriteString(`<div class="pf-dashboard-lower"><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Recent activity</p><h2>Audit events</h2></div><a class="pf-text-link" href="/operations#audit">View all</a></div>`)
	if len(audit) == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No events yet</strong><span>Mutations and policy decisions will appear here.</span></div>`)
	} else {
		b.WriteString(`<div class="pf-event-list">`)
		for _, event := range audit {
			fmt.Fprintf(&b, `<div class="pf-event"><span class="pf-event-icon"><img src="/assets/icons/activity.svg" alt=""></span><span><strong>%s</strong><small>%s · %s</small></span></div>`, safeText(event.Action), safeText(event.Actor), safeText(unixLabel(event.CreatedAt)))
		}
		b.WriteString(`</div>`)
	}
	lastReportLabel := "No reports yet"
	if latestReport > 0 {
		lastReportLabel = unixLabel(latestReport)
	}
	b.WriteString(`</section><section class="pf-surface pf-status-card"><div class="pf-section-heading"><div><p class="pf-kicker">Control health</p><h2>Tailnet status</h2></div><span class="pf-health-label"><i class="pf-status-dot pf-status-` + controlTone + `"></i> ` + safeText(controlLabel) + `</span></div><div class="pf-status-row"><span>Tailnet</span><strong class="font-mono">` + safeText(tailnet.Name) + `</strong></div><div class="pf-status-row"><span>DNS sync</span><strong>` + pill("Ownership enforced", "ok") + `</strong></div><div class="pf-status-row"><span>Last report</span><strong>` + safeText(lastReportLabel) + `</strong></div><a class="pf-secondary-button" href="/operations">Open operations <span aria-hidden="true">→</span></a></section></div>`)
	render(w, "Dashboard", b.String())
}
func (c *Control) saveBootstrap(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "invalid bootstrap settings", http.StatusBadRequest)
		return
	}
	input.URL = strings.TrimSpace(input.URL)
	if input.URL == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	var settings map[string]any
	if err := c.Store.GetJSON("tailnet", &settings); err != nil {
		settings = map[string]any{}
	}
	settings["bootstrap_url"] = input.URL
	if err := c.Store.SetJSON("tailnet", settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("settings.bootstrap", identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

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

func (c *Control) sync(w http.ResponseWriter, r *http.Request) {
	var in syncInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "invalid sync request", http.StatusBadRequest)
		return
	}
	assignment, zoneErr := c.Store.ZoneByID(in.ZoneID)
	if zoneErr != nil || assignment.ProviderID != in.ProviderID {
		http.Error(w, "Select a managed zone belonging to this DNS provider", 400)
		return
	}
	in.Zone = assignment.Zone
	if in.ProviderID == "" || in.NodeID == "" || in.ZoneID == "" {
		http.Error(w, "provider_id, zone_id and node_id are required", http.StatusBadRequest)
		return
	}
	node, nodeErr := c.syncNode(in.NodeID, len(in.Records) == 0)
	if nodeErr != nil {
		http.Error(w, nodeErr.Error(), 400)
		return
	}
	owner := node.Mode + ":" + node.ID
	if store.IsGateway(node.Mode) {
		owner = "gateway:" + node.ID
	}
	if !in.DryRun && !requestAccess(r).Admin {
		http.Error(w, "administrator access required to apply DNS changes", 403)
		return
	}
	if in.AllowUnowned && !requestAccess(r).Admin {
		http.Error(w, "administrator approval required for unowned overwrite", 403)
		return
	}
	desired := make([]libdns.Record, 0, len(in.Records))
	for _, item := range in.Records {
		if item.Name == "" || item.Type == "" {
			http.Error(w, "record name and type are required", http.StatusBadRequest)
			return
		}
		ttl := time.Duration(item.TTL) * time.Second
		desired = append(desired, libdns.RR{Name: item.Name, Type: strings.ToUpper(item.Type), Data: item.Data, TTL: ttl})
	}
	result, err := c.reconcile(r.Context(), in.ProviderID, in.Zone, owner, desired, in.DryRun, in.AllowUnowned, in.ApprovalToken)
	if err != nil {
		http.Error(w, err.Error(), resultStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	result["dry_run"] = in.DryRun
	result["node_id"] = node.ID
	_ = json.NewEncoder(w).Encode(result)
}

func (c *Control) syncNode(id string, cleanup bool) (store.NodeRecord, error) {
	node, err := c.Store.Node(id)
	if err != nil {
		return node, fmt.Errorf("unknown node")
	}
	if !store.IsGateway(node.Mode) && node.Mode != "loadbalancer" {
		return node, fmt.Errorf("node mode cannot manage exposures")
	}
	if node.Revoked && !cleanup {
		return node, fmt.Errorf("revoked nodes permit empty desired-state cleanup only")
	}
	return node, nil
}

func resultStatus(err error) int {
	if strings.Contains(err.Error(), "unowned provider record") || strings.Contains(err.Error(), "record ownership conflict") || strings.Contains(err.Error(), "overwrite approval") {
		return http.StatusConflict
	}
	if strings.Contains(err.Error(), "not found") {
		return http.StatusNotFound
	}
	if strings.Contains(err.Error(), "disabled") {
		return http.StatusConflict
	}
	if strings.Contains(err.Error(), "adapter") {
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func (c *Control) reconcile(ctx context.Context, providerID, zone, ownerID string, desired []libdns.Record, dryRun, allowUnowned bool, approvalToken ...string) (result map[string]any, failure error) {
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
		_ = c.Store.SetJSON("zone_sync:"+providerID+":"+zone, status)
	}()
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	p, config, err := c.Store.ProviderConfig(providerID)
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
	zones, err := c.Store.Zones()
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
	allLedger, err := c.Store.PartitionZoneLedger(providerID, zone)
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
	if !dryRun {
		if err := provider.Apply(ctx, zone, add, remove); err != nil {
			return nil, err
		}
		if allowUnowned {
			transferred := append(append(append([]libdns.Record{}, desired...), add...), remove...)
			if err := c.Store.TransferZoneLedgerOwnership(zone, ownerID, transferred); err != nil {
				return nil, err
			}
		}
		if err := c.Store.ReplaceLedger(p.ID, zone, ownerID, desired); err != nil {
			return nil, err
		}
		if assignment, err := c.managedZone(zone); err == nil && assignment.ProviderID == providerID {
			_ = c.Store.SetJSON("zone_last_sync:"+assignment.ID, time.Now().Unix())
		}
		_ = c.Store.Audit("dns.reconcile:"+ownerID, "control")
		if allowUnowned {
			_ = c.Store.Audit("dns.explicit-overwrite:"+ownerID, "administrator")
		}
	}
	return map[string]any{"add": len(add), "remove": len(remove), "requires_approval": false, "existing_records": managedRecordViews(existing, allLedger, ownerID), "desired_records": recordViews(desired)}, nil
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

func (c *Control) exposureSync(w http.ResponseWriter, r *http.Request) {
	var in exposureInput
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "invalid exposure", 400)
		return
	}
	assignment, zoneErr := c.Store.ZoneByID(in.ZoneID)
	if zoneErr != nil || assignment.ProviderID != in.ProviderID {
		http.Error(w, "Select a managed zone belonging to this DNS provider", 400)
		return
	}
	in.Zone = assignment.Zone
	if in.ProviderID == "" || in.NodeID == "" || in.ZoneID == "" {
		http.Error(w, "provider_id, zone_id and node_id are required", 400)
		return
	}
	node, nodeErr := c.syncNode(in.NodeID, in.Cleanup)
	if nodeErr != nil {
		http.Error(w, nodeErr.Error(), 400)
		return
	}
	owner := node.Mode + ":" + node.ID
	if store.IsGateway(node.Mode) {
		owner = "gateway:" + node.ID
	}
	if !in.DryRun && !requestAccess(r).Admin {
		http.Error(w, "administrator access required to apply DNS changes", 403)
		return
	}
	if in.AllowUnowned && !requestAccess(r).Admin {
		http.Error(w, "administrator approval required for unowned overwrite", 403)
		return
	}
	var records []libdns.Record
	var err error
	if in.Cleanup {
	} else if node.Mode == "loadbalancer" {
		var record libdns.Record
		record, _, err = dns.LoadBalancerRecord(in.LoadBalancer)
		if err == nil {
			records = []libdns.Record{record}
		}
	} else {
		err = fmt.Errorf("kind must be loadbalancer or gateway")
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	result, err := c.reconcile(r.Context(), in.ProviderID, in.Zone, owner, records, in.DryRun, in.AllowUnowned, in.ApprovalToken)
	if err != nil {
		http.Error(w, err.Error(), resultStatus(err))
		return
	}
	result["dry_run"] = in.DryRun
	result["node_id"] = node.ID
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (c *Control) exposures(w http.ResponseWriter, r *http.Request) { c.exposureMappingPage(w, r) }

func (c *Control) reports(w http.ResponseWriter, _ *http.Request) {
	reports, err := c.Store.SignedReports()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reports)
}

func (c *Control) audit(w http.ResponseWriter, _ *http.Request) {
	events, err := c.Store.AuditEvents(100)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(events)
}

func (c *Control) enrolledNode(r *http.Request, id, mode string) (store.NodeRecord, error) {
	node, err := c.Store.Node(id)
	if err != nil || node.Revoked || (mode != "" && node.Mode != mode) || node.TailnetID == "" || node.TailnetID != principal(r).NodeID {
		return node, fmt.Errorf("unknown, revoked, or mismatched node identity")
	}
	return node, nil
}
func (c *Control) callerNode(r *http.Request, mode string) (store.NodeRecord, error) {
	p := principal(r)
	if p.NodeID == "" {
		return store.NodeRecord{}, fmt.Errorf("verified tailnet node identity required")
	}
	node, err := c.Store.NodeByTailnetID(p.NodeID)
	if err != nil || node.Revoked || (mode != "" && node.Mode != mode) {
		return node, fmt.Errorf("unknown, revoked, or mismatched node identity")
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
		if e != nil || parent.Revoked {
			return node, fmt.Errorf("controller revoked")
		}
	}
	return node, nil
}
func applicationAllows(app store.ApplicationRecord, node store.NodeRecord) bool {
	return app.Enabled && !node.Revoked && store.IsGateway(node.Mode) && (app.BackendSource != "tailnet" || len(app.AllowedGatewayNodes) == 1) && (app.BackendSource == store.BackendSource(node.Mode) || (app.BackendSource == "" && node.Mode == "docker-gateway")) && (len(app.AllowedGatewayNodes) == 0 || contains(app.AllowedGatewayNodes, node.ID) || node.ParentID != "" && contains(app.AllowedGatewayNodes, node.ParentID))
}
func (c *Control) nodeConfig(w http.ResponseWriter, r *http.Request) {
	if n, err := c.callerNode(r, "loadbalancer"); err == nil && n.Mode == "loadbalancer" {
		c.loadBalancerConfig(w, r)
		return
	}
	node, err := c.callerGateway(r)
	id := node.ID
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, principal(r).RoleSelectors())
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	deployment, err := c.nodeDeployment(node.ID)
	if err != nil {
		http.Error(w, "Unable to read node settings", 503)
		return
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
			if c.anyEdgeAllows(c.loadExposurePolicy(), host, []string{id, node.TailnetID, node.ParentID}, principal(r).RoleSelectors()) {
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
	pub, key, err := c.Store.ConfigurationKey()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	envelope, err := report.NewEnvelope(id, config, pub, key, time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)
}

func (c *Control) nodeConfigAck(w http.ResponseWriter, r *http.Request) {
	node, err := c.callerNode(r, "")
	id := node.ID
	p := principal(r)
	if err != nil {
		http.Error(w, "node identity does not match configuration", http.StatusForbidden)
		return
	}
	var input struct {
		Revision string   `json:"revision"`
		Services []string `json:"services"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input) != nil || input.Revision == "" {
		http.Error(w, "invalid configuration acknowledgement", 400)
		return
	}
	_ = c.Store.SetJSON("node_config_ack:"+id, map[string]any{"revision": input.Revision, "services": input.Services, "at": time.Now().Unix()})
	_ = c.Store.Audit("node.config.ack:"+id, "node:"+p.NodeID)
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) nodeHealth(w http.ResponseWriter, r *http.Request) {
	node, err := c.callerNode(r, "")
	id := node.ID
	if err != nil {
		http.Error(w, "node identity does not match health report", http.StatusForbidden)
		return
	}
	var health map[string]any
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&health) != nil {
		http.Error(w, "invalid health report", 400)
		return
	}
	delete(health, "public_ipv4")
	health["received_at"] = time.Now().Unix()
	health["effective_tags"] = principal(r).Tags
	if err := c.Store.TouchNodeReport(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.Store.SetJSON("node_health:"+id, health); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if node.Mode == "loadbalancer" {
		if syncErr := c.reconcileLoadBalancerNode(r.Context(), node); syncErr != nil {
			// Endpoint ambiguity deliberately does not delete the last owned A
			// record. Keep the diagnostic beside health so the UI can explain
			// why DNS was retained without failing the node heartbeat.
			health["dns_sync_error"] = syncErr.Error()
		}
	}
	if err := c.Store.SetJSON("node_health:"+id, health); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) nodeShutdown(w http.ResponseWriter, r *http.Request) {
	node, err := c.callerNode(r, "")
	if err != nil {
		http.Error(w, "node identity does not match shutdown request", http.StatusForbidden)
		return
	}
	if err := c.Store.RevokeNode(node.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.RevokeBackends(node.ID)
	_ = c.Store.DeleteReport(node.ID)
	node.Revoked = true
	if err := c.cleanupRevokedNode(r.Context(), node); err != nil {
		writeJSON(w, map[string]any{"revoked": true, "pending": true, "error": err.Error()})
		return
	}
	_ = c.Store.Audit("node.shutdown:"+node.ID, "node:"+principal(r).NodeID)
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) tailscaleRuntimeClient() (tailscale.Client, error) {
	var settings struct {
		Name string `json:"name"`
	}
	if err := c.Store.GetJSON("tailnet", &settings); err != nil || strings.TrimSpace(settings.Name) == "" {
		return tailscale.Client{}, fmt.Errorf("tailnet is not configured")
	}
	id, err := c.Store.GetSecret("tailscale_oauth_client_id")
	if err != nil {
		return tailscale.Client{}, err
	}
	secret, err := c.Store.GetSecret("tailscale_oauth_client_secret")
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
	if stale {
		return c.reconcilePartitions(ctx, ownerID, nil)
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
	client, err := c.tailscaleRuntimeClient()
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

// ReconcileLoadBalancerDNS removes owned A records after a loadbalancer health
// report expires and reapplies a verified endpoint on the periodic control
// loop. Missing or ambiguous fresh endpoints are retained by design.
func (c *Control) ReconcileLoadBalancerDNS(ctx context.Context) error {
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
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

func (c *Control) applicationList(w http.ResponseWriter, _ *http.Request) {
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apps)
}

func (c *Control) applications(w http.ResponseWriter, r *http.Request) {
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	serviceHosts, _ := c.Store.ServiceHosts()
	hostState := map[string][]string{}
	for _, host := range serviceHosts {
		state := host.ApprovalState
		if state == "" {
			state = "pending"
		}
		hostState[host.ServiceName] = append(hostState[host.ServiceName], host.DeviceID+" · "+state)
	}
	var b strings.Builder
	b.WriteString(pageIntro("Infrastructure", "Applications", "Applications are provisioned automatically from Docker labels authorized by exposure policy. This inventory shows routing, Service approval, and health."))
	b.WriteString(`<div class="pf-alert pf-alert-info"><strong>Policy-controlled discovery.</strong> Configure an exposure policy once, then add <code>tailscale-private-funnel.expose=5173:app.example.com:http</code> to a container. Control creates the application and Tailscale Service automatically. <a href="/policies">Manage exposure policy</a></div>`)
	b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Approved services</p><h2>Application inventory</h2></div><span class="pf-pill pf-pill-neutral">` + fmt.Sprintf("%d total", len(apps)) + `</span><a class="pf-text-link" href="/api/services">Service advertisements JSON</a></div>`)
	if len(apps) == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No applications yet</strong><span>Add a Docker exposure label covered by an enabled exposure policy.</span></div>`)
	} else {
		b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Application</th><th>Service</th><th>Public hostnames</th><th>Ports</th><th>Gateway allowlist</th><th>State</th></tr></thead><tbody>`)
		for _, app := range apps {

			state, tone := "Disabled", "neutral"
			if app.Enabled {
				state, tone = "Enabled", "ok"
			}
			allowlist := ""
			for _, id := range app.AllowedGatewayNodes {
				if allowlist != "" {
					allowlist += ", "
				}
				allowlist += c.nodeDisplayName(id)
			}
			if allowlist == "" {
				allowlist = "Any enrolled gateway"
			}
			advertisements := strings.Join(hostState[app.ServiceName], ", ")
			if advertisements == "" {
				advertisements = "no advertisements"
			}
			fmt.Fprintf(&b, `<tr><td data-label="Application"><strong>%s</strong><small class="pf-muted">policy rev %d</small></td><td data-label="Service"><code>%s</code><small class="pf-muted">target · %s</small><small class="pf-muted">hosts · %s</small></td><td data-label="Public hostnames"><code>%s</code></td><td data-label="Ports"><code>%d → %d</code></td><td data-label="Gateway allowlist"><span class="pf-muted">%s</span></td><td data-label="State">%s</td></tr>`, safeText(app.Name), app.PolicyRevision, safeText(app.ServiceName), safeText(app.ServiceFQDN), safeText(advertisements), safeText(strings.Join(app.PublicHostnames, ", ")), app.BackendPort, app.ServicePort, safeText(allowlist), pill(state, tone))
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</section>`)
	render(w, "Applications", b.String())
}

func (c *Control) serviceInventory(w http.ResponseWriter, r *http.Request) {
	settings := struct {
		Name string `json:"name"`
	}{}
	if c.Store.GetJSON("tailnet", &settings) != nil || strings.TrimSpace(settings.Name) == "" {
		http.Error(w, "configure the tailnet first", http.StatusPreconditionRequired)
		return
	}
	clientID, err := c.Store.GetSecret("tailscale_oauth_client_id")
	if err != nil {
		http.Error(w, "configure Tailscale OAuth first", http.StatusPreconditionRequired)
		return
	}
	clientSecret, err := c.Store.GetSecret("tailscale_oauth_client_secret")
	if err != nil {
		http.Error(w, "configure Tailscale OAuth first", http.StatusPreconditionRequired)
		return
	}
	client := c.tsClient(clientID, clientSecret, settings.Name)
	services, err := client.ListServices(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	type serviceView struct {
		Service tailscale.Service       `json:"service"`
		Hosts   []tailscale.ServiceHost `json:"hosts"`
	}
	views := make([]serviceView, 0, len(services))
	for _, service := range services {
		hosts, hostErr := client.ListServiceHosts(r.Context(), service.Name)
		if hostErr != nil {
			http.Error(w, hostErr.Error(), http.StatusBadGateway)
			return
		}
		views = append(views, serviceView{Service: service, Hosts: hosts})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"services": views})
}

func (c *Control) loadBalancerConfig(w http.ResponseWriter, r *http.Request) {
	node, err := c.Store.NodeByTailnetID(principal(r).NodeID)
	if err != nil || node.Revoked || node.Mode != "loadbalancer" {
		http.Error(w, "enrolled LoadBalancer identity required", 403)
		return
	}
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	routes := []proxy.Route{}
	config := nodeconfig.Configuration{NodeID: node.ID}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, principal(r).RoleSelectors())
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
	pub, key, err := c.Store.ConfigurationKey()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	envelope, err := report.NewEnvelope(node.ID, config, pub, key, time.Now())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(envelope)

}

func (c *Control) applicationSave(w http.ResponseWriter, r *http.Request) {
	var app store.ApplicationRecord
	formResponse := !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
	if formResponse {
		app.Name = r.FormValue("name")
		app.ID = r.FormValue("id")
		app.ServiceName = r.FormValue("service_name")
		app.ServiceFQDN = r.FormValue("service_fqdn")
		app.PublicHostnames = splitValues(r.FormValue("public_hostnames"))
		app.AllowedGatewayNodes = splitValues(r.FormValue("allowed_gateway_nodes"))
		app.BackendPort = uint16(parsePositiveInt(r.FormValue("backend_port")))
		app.ServicePort = uint16(parsePositiveInt(r.FormValue("service_port")))
	} else if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&app) != nil {
		http.Error(w, "invalid application", 400)
		return
	}
	if app.ID != "" {
		http.Error(w, "Application IDs are server generated", 400)
		return
	}
	app.Name = ""
	var err error
	app.ID, err = store.NewResourceID()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if app.ServiceName == "" {
		sum := sha256.Sum256([]byte(app.ID))
		app.ServiceName = fmt.Sprintf("svc:tailscale-private-funnel-app-%x", sum[:12])
	}
	if !serviceNamePattern.MatchString(app.ServiceName) || len(app.ServiceName) > 64 {
		http.Error(w, "service_name must use a safe svc: name", 400)
		return
	}
	if app.ServiceFQDN != "" && validateServiceTarget(app.ServiceFQDN) != nil {
		http.Error(w, "service_fqdn must be a Tailscale Service hostname or IP address", 400)
		return
	}
	if app.ServicePort == 0 {
		app.ServicePort = app.BackendPort
	}
	if app.BackendPort == 0 {
		http.Error(w, "backend_port is required", 400)
		return
	}
	if len(app.PublicHostnames) == 0 {
		http.Error(w, "at least one public hostname is required", 400)
		return
	}
	seenHostnames := map[string]bool{}
	for i, hostname := range app.PublicHostnames {
		hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
		if err := policy.ValidateHostname(hostname); err != nil {
			http.Error(w, fmt.Sprintf("public_hostnames[%d]: %v", i, err), 400)
			return
		}
		if seenHostnames[hostname] {
			http.Error(w, "public_hostnames must be unique", 400)
			return
		}
		seenHostnames[hostname] = true
		app.PublicHostnames[i] = hostname
	}
	existing, e := c.Store.Applications()
	if e != nil {
		http.Error(w, "Unable to read applications", 503)
		return
	}
	for _, candidate := range existing {
		if candidate.ID == app.ID && candidate.BackendSource == "tailnet" {
			http.Error(w, "Manage this assignment through Tailnet exposures", 400)
			return
		}
	}
	if app.BackendSource != "" && app.BackendSource != "docker" {
		http.Error(w, "Use tailnet exposure management", 400)
		return
	}
	settings := struct {
		Name string `json:"name"`
	}{}
	if c.Store.GetJSON("tailnet", &settings) != nil || settings.Name == "" {
		http.Error(w, "configure the tailnet first", http.StatusPreconditionRequired)
		return
	}
	clientID, err := c.Store.GetSecret("tailscale_oauth_client_id")
	if err != nil {
		http.Error(w, "configure Tailscale OAuth first", 428)
		return
	}
	clientSecret, err := c.Store.GetSecret("tailscale_oauth_client_secret")
	if err != nil {
		http.Error(w, "configure Tailscale OAuth first", 428)
		return
	}
	service, err := (c.tsClient(clientID, clientSecret, settings.Name)).CreateService(r.Context(), tailscale.Service{Name: app.ServiceName, DisplayName: app.Name, Ports: []string{fmt.Sprintf("tcp:%d", app.ServicePort)}, Tags: []string{"tag:tailscale-private-funnel"}})
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	if target := selectServiceTarget(service.Addresses, app.ServiceName, settings.Name); target != "" {
		app.ServiceFQDN = target
	}
	app.Enabled = true
	if err := c.Store.UpsertApplication(app); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("application.save:"+app.ID, identityFromRequest(r))
	if formResponse {
		if r.Header.Get("HX-Request") == "true" {
			fmt.Fprintf(w, `<span class="pf-result-success">Service %s created. Add its generated label to the approved application.</span>`, safeText(app.ID))
			return
		}
		http.Redirect(w, r, "/applications", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(app)
}

func parsePositiveInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	if parsed < 0 || parsed > 65535 {
		return 0
	}
	return parsed
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

func (c *Control) serviceApprove(w http.ResponseWriter, r *http.Request) {
	serviceName, deviceID := chi.URLParam(r, "service"), chi.URLParam(r, "device")
	if serviceName == "" || deviceID == "" {
		http.Error(w, "service and device are required", 400)
		return
	}
	settings := struct {
		Name string `json:"name"`
	}{}
	if c.Store.GetJSON("tailnet", &settings) != nil || settings.Name == "" {
		http.Error(w, "configure the tailnet first", 428)
		return
	}
	clientID, err := c.Store.GetSecret("tailscale_oauth_client_id")
	if err != nil {
		http.Error(w, "configure Tailscale OAuth first", 428)
		return
	}
	clientSecret, err := c.Store.GetSecret("tailscale_oauth_client_secret")
	if err != nil {
		http.Error(w, "configure Tailscale OAuth first", 428)
		return
	}
	var input struct {
		Approved bool `json:"approved"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&input) != nil {
		http.Error(w, "invalid approval", 400)
		return
	}
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var application store.ApplicationRecord
	var applicationFound bool
	for _, candidate := range apps {
		if candidate.ServiceName == serviceName {
			application, applicationFound = candidate, true
			break
		}
	}
	client := c.tsClient(clientID, clientSecret, settings.Name)
	allowed := false
	if applicationFound {
		allowed = c.serviceHostAllowed(r.Context(), client, application, deviceID)
	}
	if !applicationFound || (input.Approved && !allowed) {
		http.Error(w, "Service host is not an approved gateway assignment", http.StatusForbidden)
		return
	}
	approved, err := (c.tsClient(clientID, clientSecret, settings.Name)).ApproveServiceDevice(r.Context(), serviceName, deviceID, input.Approved)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	_ = c.Store.UpsertServiceHost(store.ServiceHostRecord{ServiceName: serviceName, DeviceID: deviceID, Approved: approved, ApprovalState: map[bool]string{true: "approved", false: "revoked"}[approved]})
	_ = c.Store.Audit("service.approval:"+serviceName+":"+deviceID, identityFromRequest(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"approved": approved})
}

// ReconcileServiceApprovals is intentionally conservative. It only approves a
// pending host when the Service is an application created in this control
// store, the device is an enrolled non-revoked gateway node, and the
// application explicitly allows that node. Unknown advertisements are left
// pending for an administrator to inspect.
func (c *Control) ReconcileServiceApprovals(ctx context.Context) error {
	c.retryNodeCleanup(ctx)
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
			allowed := false
			for _, app := range owners {
				allowed = allowed || (app.Enabled && c.serviceHostAllowed(ctx, client, app, host.NodeID))
			}
			approved := host.ApprovalLevel == "approved:auto" || host.ApprovalLevel == "approved:manual"
			if allowed != approved {
				if _, e := client.ApproveServiceDevice(ctx, service, host.NodeID, allowed); e != nil {
					failures = append(failures, e.Error())
					continue
				}
				approved = allowed
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

// Service advertisements belong to the enrolled gateway device itself.
func (c *Control) serviceHostAllowed(ctx context.Context, client tailscale.Client, app store.ApplicationRecord, deviceID string) bool {
	node, err := c.Store.NodeByTailnetID(deviceID)
	if err != nil || !applicationAllows(app, node) {
		return false
	}
	if node.Mode == "k8s-gateway" {
		children, e := c.Store.Forwarders(node.ID)
		if e != nil || len(children) > 0 {
			return false
		}
	}
	if node.ParentID != "" {
		parent, e := c.Store.Node(node.ParentID)
		if e != nil || parent.Revoked {
			return false
		}
	}
	active := false
	for _, exposure := range c.freshReport(node) {
		if exposure.ApplicationID == app.ID {
			active = true
			break
		}
	}
	if !active {
		return false
	}
	var ack struct {
		Services []string `json:"services"`
		At       int64    `json:"at"`
	}
	if c.Store.GetJSON("node_config_ack:"+node.ID, &ack) != nil || !contains(ack.Services, app.ServiceName) || ack.At < time.Now().Add(-5*time.Minute).Unix() {
		return false
	}
	device, err := client.GetDevice(ctx, deviceID)
	if err == nil {
		allowed := false
		for _, host := range app.PublicHostnames {
			allowed = allowed || c.anyEdgeAllows(c.loadExposurePolicy(), host, []string{node.ID, node.TailnetID, node.ParentID}, device.Tags)
		}
		if !allowed {
			return false
		}
	}
	return err == nil && device.Authorized && (device.NodeID == "" || device.NodeID == deviceID) && contains(device.Tags, "tag:tailscale-private-funnel")
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
	ids, err := c.Store.StaleReportIDs(time.Now().Add(-maxAge).Unix())
	if err != nil {
		return 0, err
	}
	removed := 0
	var failures []string
	for _, id := range ids {
		_ = c.Store.RevokeBackends(id)
		if err := c.reconcilePartitions(ctx, "gateway:"+id, nil); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if err := c.Store.DeleteReport(id); err != nil {
			return removed, err
		}
		removed++
	}
	if len(failures) > 0 {
		return removed, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return removed, nil
}

// CleanupStaleNodes removes runtime enrollments that stopped heartbeating.
// The control node is never selected by Store.StaleNodes.
func (c *Control) CleanupStaleNodes(ctx context.Context, maxAge time.Duration) (int, error) {
	nodes, err := c.Store.StaleNodes(time.Now().Add(-maxAge).Unix())
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, node := range nodes {
		_ = c.Store.SetJSON("node_offline:"+node.ID, map[string]any{"offline": true, "last_report": node.LastReport})
		_ = c.Store.RevokeBackends(node.ID)
		_ = c.Store.DeleteReport(node.ID)
		removed++
	}
	// Persistent devices survive outages. Only explicit reset or revocation
	// deletes an identity; report expiry still withdraws routes and owned DNS.
	_ = c.reconcileExposures(ctx)

	return removed, nil
}

func (c *Control) expireReports(w http.ResponseWriter, r *http.Request) {
	removed, err := c.ExpireReports(r.Context(), 5*time.Minute)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	_ = c.Store.Audit("reports.expire", identityFromRequest(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"removed": removed})
}

func (c *Control) saveReport(w http.ResponseWriter, r *http.Request) {
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
	if err := c.policyStorageError(); err != nil {
		http.Error(w, "Exposure policy unavailable; retry later", 503)
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	var envelope report.Envelope
	if err := dec.Decode(&envelope); err != nil {
		http.Error(w, "invalid report", http.StatusBadRequest)
		return
	}
	if _, _, err := report.Verify(envelope, time.Now(), 5*time.Minute); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	if reportHasRemovedEdgeField(envelope.Payload) {
		http.Error(w, "loadbalancer_id is not accepted in runtime reports", 400)
		return
	}
	p := principal(r)
	node, nodeErr := c.Store.Node(strings.TrimSpace(envelope.NodeID))
	if nodeErr == nil && node.Mode == "loadbalancer" {
		c.saveDirectReport(w, r, envelope, node)
		return
	}
	if p.NodeID == "" || nodeErr != nil || !store.IsGateway(node.Mode) || node.TailnetID == "" || node.TailnetID != p.NodeID {
		http.Error(w, fmt.Sprintf("report node does not match enrolled tailnet identity (report=%s whois=%s stored=%s err=%v)", envelope.NodeID, p.NodeID, node.TailnetID, nodeErr), 403)
		return
	}
	if node.ParentID != "" {
		parent, e := c.Store.Node(node.ParentID)
		if e != nil || parent.Revoked {
			http.Error(w, "controller revoked", 403)
			return
		}
	}
	revoked, err := c.Store.NodeRevoked(strings.TrimSpace(envelope.NodeID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if revoked {
		http.Error(w, "unknown or revoked node", http.StatusForbidden)
		return
	}
	var freshness struct {
		ExpiresAt int64 `json:"expires_at"`
	}
	if err := json.Unmarshal(envelope.Payload, &freshness); err != nil || freshness.ExpiresAt <= time.Now().Unix() || freshness.ExpiresAt > envelope.Timestamp+int64(5*time.Minute/time.Second) {
		http.Error(w, "invalid report expiry", 400)
		return
	}
	var reportPayload struct {
		ProviderID string                `json:"provider_id"`
		Exposures  []dns.GatewayExposure `json:"exposures"`
	}
	if err := json.Unmarshal(envelope.Payload, &reportPayload); err != nil {
		http.Error(w, "invalid report payload", http.StatusBadRequest)
		return
	}
	var zoneFailures []string
	gatewayIDs := []string{node.ID, node.ParentID}
	roles := p.RoleSelectors()
	applications, applicationsErr := c.Store.Applications()
	if applicationsErr != nil {
		http.Error(w, applicationsErr.Error(), http.StatusInternalServerError)
		return
	}
	reportPayload.Exposures = c.expandDockerAliases(reportPayload.Exposures, applications, node, roles)
	for i := range reportPayload.Exposures {
		exposure := &reportPayload.Exposures[i]
		application, applicationOK := approvedApplication(applications, *exposure)
		if !applicationOK || !applicationAllows(application, node) {
			http.Error(w, "exposure does not match an enabled application Service", http.StatusForbidden)
			return
		}
		if store.BackendSource(node.Mode) != application.BackendSource && !(node.Mode == "docker-gateway" && application.BackendSource == "") {
			http.Error(w, "gateway backend source mismatch", 403)
			return
		}
		if !exposure.Reachable {
			continue
		}
		exposure.ApplicationID = application.ID
		exposure.ServiceName = application.ServiceName
		if !c.applicationAnyEdgeAllowed(application, gatewayIDs, roles) {
			http.Error(w, "gateway exposure is not allowed by the control policy", 403)
			return
		}
		if _, err := c.managedZone(exposure.PublicHostname); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := c.Store.AcceptReport(strings.TrimSpace(envelope.NodeID), envelope.Nonce, payload, envelope.PublicKey); err != nil {
		if strings.Contains(err.Error(), "nonce already used") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, roles)
	activeBackends := map[string]bool{}
	for _, exposure := range reportPayload.Exposures {
		activeBackends[backendRecordID(envelope.NodeID, exposure)] = true
		if err := c.Store.UpsertBackend(store.BackendAssignment{
			ID: backendRecordID(envelope.NodeID, exposure), ApplicationID: exposure.ApplicationID, NodeID: envelope.NodeID,
			ContainerID: exposure.ContainerID, BackendIP: exposure.BackendIP, BackendPort: exposure.Port,
			Networks: exposure.Networks, Reachable: exposure.Reachable, ConfigRevision: envelope.Timestamp,
			LastSeen: time.Now().Unix(),
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := c.Store.ReconcileBackends(envelope.NodeID, activeBackends); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Child heartbeats must not serialize external DNS calls per cluster node.
	// The control reconciliation loop applies the aggregate on its next tick.
	if node.Mode == "k8s-forwarder" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if err := c.reconcileExposures(r.Context()); err != nil {
		zoneFailures = append(zoneFailures, err.Error())
	}
	if len(zoneFailures) > 0 {
		http.Error(w, "Report accepted; independent valid zones reconciled. DNS cleanup pending: "+strings.Join(zoneFailures, "; "), 502)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func backendRecordID(nodeID string, exposure dns.GatewayExposure) string {
	return strings.Join([]string{nodeID, exposure.ServiceName, exposure.PublicHostname, fmt.Sprintf("%d", exposure.Port)}, ":")
}

func approvedApplication(applications []store.ApplicationRecord, exposure dns.GatewayExposure) (store.ApplicationRecord, bool) {
	hostname := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(exposure.PublicHostname), "."))
	for _, application := range applications {
		if !application.Enabled || application.ID != exposure.ApplicationID || application.ServiceName != strings.TrimSpace(exposure.ServiceName) || application.BackendPort != exposure.Port {
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
func (c *Control) nodes(w http.ResponseWriter, r *http.Request) {
	mode := "loadbalancer"
	if r.URL.Path == "/gateways" {
		mode = "docker-gateway"
	}
	nodes, _ := c.Store.Nodes(mode)
	title, kicker, description, href := "Load balancers", "Infrastructure", "The public edge terminates TLS, connects to Docker Services through gateway, and routes tailnet machines directly by verified device identity.", "/enroll/loadbalancer"
	if mode == "docker-gateway" {
		title, description, href = "Gateways", "Gateways expose policy-approved Docker HTTP backends through Tailscale Services and report signed desired state to control.", "/enroll/docker-gateway"
	}
	var b strings.Builder
	b.WriteString(pageIntro(kicker, title, description))
	if requestAccess(r).Admin {
		b.WriteString(`<div class="pf-action-row"><a class="pf-button" href="` + href + `">Create enrollment</a></div>`)
	}
	b.WriteString(`<div class="pf-action-row"><a class="pf-text-link" href="/api/nodes?mode=` + mode + `">View technical details (JSON)</a></div><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Node inventory</p><h2>Connected ` + strings.ToLower(title) + `</h2></div><span class="pf-pill pf-pill-neutral">` + fmt.Sprintf("%d total", len(nodes)) + `</span></div>`)
	if len(nodes) == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No ` + strings.ToLower(title) + ` enrolled</strong><span>Use the generated deployment script to provision a node, then return here to confirm its identity and report state.</span></div>`)
	} else {
		b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Name / hostname</th><th>Tailnet identity</th><th>Health</th><th>Last report</th><th>Action</th></tr></thead><tbody>`)
		for _, node := range nodes {
			revoked := node["revoked"] == true
			last := int64(0)
			if value, ok := node["last_report"].(int64); ok {
				last = value
			}
			state, tone := "Awaiting report", "warn"
			if revoked {
				state, tone = "Revoked", "error"
			} else if last > 0 {
				state, tone = "Healthy", "ok"
				freshness := c.ReportTTL
				if freshness <= 0 {
					freshness = 5 * time.Minute
				}
				if time.Since(time.Unix(last, 0)) > freshness {
					state, tone = "Stale", "warn"
				}
			}
			nodeID := stringValue(node, "id", "")
			action := `<div class="pf-inline-actions"><a class="pf-text-link" href="/nodes/` + safeText(nodeID) + `/settings">Node details</a>`
			if !revoked && nodeID != "" && requestAccess(r).Admin {
				action += `<form method="post" action="/api/nodes/` + safeText(nodeID) + `/revoke"><button class="pf-link-button pf-link-danger" type="submit">Revoke</button></form>`
			}
			action += `</div>`
			fmt.Fprintf(&b, `<tr><td data-label="Name / hostname"><strong>%s</strong><small class="pf-muted">%s</small></td><td data-label="Tailnet identity"><code>%s</code></td><td data-label="Health">%s</td><td data-label="Last report"><span class="pf-muted">%s</span></td><td data-label="Action">%s</td></tr>`, safeText(c.nodeDisplayName(stringValue(node, "id", ""))), safeText(stringValue(node, "mode", mode)), safeText(nodeID), pill(state, tone), safeText(unixLabel(last)), action)
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</section>`)
	if requestAccess(r).Admin {
		enrollments, _ := c.Store.Enrollments(mode)
		if len(enrollments) > 0 {
			b.WriteString(`<section class="pf-surface"><h2>Enrollments</h2>`)
			for _, enrollment := range enrollments {
				if mode == "docker-gateway" {
					b.WriteString(`<p><code>` + safeText(enrollment.Hostname) + `</code></p>`)
				} else {
					b.WriteString(renameForm("enrollments", enrollment.ID, enrollment.Name))
				}
			}
			b.WriteString(`</section>` + renameScript)
		}
	}
	if mode == "docker-gateway" {
		b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Gateway guidance</p><h2>Keep the route private</h2></div></div><p class="pf-muted">The supervisor automatically selects user-defined networks on each labeled target; set local deny patterns for sensitive networks. Use <code>tailscale-private-funnel.expose=PORT:hostname:http</code> labels. The gateway supervisor forwards approved Services directly to reachable container addresses on locally allowed Docker networks. A hostname must match an enabled policy rule before DNS changes.</p></section>`)
	}
	render(w, title, b.String())
}
func (c *Control) enroll(w http.ResponseWriter, r *http.Request) {
	if !requestAccess(r).Admin {

		render(w, "Enrollment", pageIntro("Infrastructure", "Enrollment", "An administrator can create a node enrollment."))
		return
	}
	mode := chi.URLParam(r, "mode")
	if store.IsGateway(mode) {
		c.gatewayEnrollment(w, r)
		return
	}
	if mode != "loadbalancer" && !store.IsGateway(mode) {
		http.Error(w, "invalid mode", 400)
		return
	}
	description := "Enroll a persistent load balancer using a native Linux service or the Kubernetes Helm chart. Each node has its own identity and certificate storage."
	if mode == "docker-gateway" {
		description = "The generated deployment script joins Tailscale, installs a system service, and starts Docker discovery. Docker socket access is required. Every public hostname must satisfy exposure policy."
	}
	var defaults map[string]any
	if err := c.Store.GetJSON("tailnet", &defaults); err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Unable to read enrollment defaults", 503)
		return
	}
	fields := `<label class="pf-field"><span class="pf-label">Load-balancer DNS hostname</span><input class="pf-input" name="loadbalancer_hostname" value="` + safeText(stringValue(defaults, "loadbalancer_hostname", "")) + `" required><small class="pf-help">Select the public edge hostname for this node.</small></label>`
	if mode == "docker-gateway" {
		fields = ""
	}
	if mode == "loadbalancer" {
		fields += `<label class="pf-field"><span class="pf-label">Deployment</span><select class="pf-input" name="deployment_target" id="lb-target"><option value="native">Native Linux service</option><option value="kubernetes">Kubernetes · Helm</option></select></label><fieldset id="lb-kubernetes" class="pf-field-wide pf-form-grid" hidden disabled><label class="pf-field"><span class="pf-label">Installation</span><input class="pf-input" name="installation_name" value="main" maxlength="31"></label><label class="pf-field"><span class="pf-label">Helm release (optional)</span><input class="pf-input" name="helm_release" placeholder="Generated unique release"></label><p class="pf-help">Shares tailscale-private-funnel-&lt;installation&gt;-system with trusted control and gateways. Requires an existing Kubernetes LoadBalancer implementation; identity persists in a Secret; Caddy storage persists on a dedicated PVC.</p></fieldset><script>document.addEventListener('DOMContentLoaded',()=>{const s=document.getElementById('lb-target'),f=document.getElementById('lb-kubernetes');s.onchange=()=>{f.hidden=s.value!=='kubernetes';f.disabled=f.hidden}})</script>`
		fields += `<label class="pf-field"><span class="pf-label">Certificate email (optional)</span><input class="pf-input" type="email" name="cert_email" value="` + safeText(stringValue(defaults, "cert_email", "")) + `"></label><p class="pf-help">Native deployment uses Tailscale endpoint discovery. Kubernetes uses its public LoadBalancer Service address. Unavailable addresses preserve previous owned records.</p>`
	} else {
		fields += `<label class="pf-field"><span class="pf-label">Additional Docker label keys (optional, comma-separated)</span><input class="pf-input" name="label_keys"></label><label class="pf-field"><span class="pf-label">Local Docker deny patterns (optional, comma-separated)</span><input class="pf-input" name="deny_networks"></label><p class="pf-help">These are local gateway deployment choices. The generated script persists them; they do not hot-reload from global settings.</p>`
	}
	enrollmentTitle, inventoryPath, inventoryTitle := "Load balancer", "/loadbalancers", "load balancers"
	if mode == "docker-gateway" {
		enrollmentTitle, inventoryPath, inventoryTitle = "Gateway", "/gateways", "gateway nodes"
	}
	identityFields := `<label class="pf-field"><span class="pf-label">Name</span><input class="pf-input" name="name" required maxlength="128"></label><label class="pf-field"><span class="pf-label">Node hostname</span><input class="pf-input" name="hostname" placeholder="Target host hostname + -loadbalancer"></label>`
	if mode == "docker-gateway" {
		identityFields = `<label class="pf-field"><span class="pf-label">Node hostname (optional)</span><input class="pf-input" name="hostname" placeholder="Target host hostname + -gateway"><small class="pf-help">Leave blank to use the target machine’s hostname followed by -gateway. The installer resolves it on that machine; this is also the node’s display name.</small></label>`
	}
	render(w, "Enrollment", pageIntro("Infrastructure", enrollmentTitle+" enrollment", description)+fmt.Sprintf(`<div class="pf-alert pf-alert-info"><strong>One-use enrollment.</strong> The bootstrap key expires in 15 minutes. The joined identity survives reboot; only reset or revocation removes it. OAuth credentials stay in the control service.</div><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Step 1 · Identity</p><h2>Choose a node hostname</h2></div><span class="pf-pill pf-pill-neutral">1 of 2</span></div><form id="enrollment-form" class="pf-form-grid">`+identityFields+fields+`<div class="pf-form-actions"><button class="pf-button" type="submit">Generate deployment script <span aria-hidden="true">→</span></button></div></form></section><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Step 2 · Deploy</p><h2>Generated deployment</h2></div><span class="pf-pill pf-pill-neutral">Linux + systemd</span></div><p class="pf-muted">Native deployment installs a verified binary and system service. Kubernetes deployment creates a short-lived bootstrap Secret and installs the pinned OCI Helm chart.</p><pre class="pf-command" id="enrollment-result" aria-live="polite">Complete the hostname step to generate a deployment script.</pre></section><p><a class="pf-text-link" href="%s">Back to %s <span aria-hidden="true">→</span></a></p><script>document.getElementById('enrollment-form').onsubmit=async function(e){e.preventDefault();const result=document.getElementById('enrollment-result');const r=await fetch('/api/enrollments/%s',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(Object.fromEntries(new FormData(this)))});const text=await r.text();if(!r.ok){result.textContent=text;return}let data;try{data=JSON.parse(text)}catch(_){result.textContent=text;return}const lines=['Enrollment created. Keep the code private until the target node has joined.','','Steps:',...(data.instructions||[]).map((step,i)=>(i+1)+'. '+step),'','One-copy deployment script:',data.deployment_script||'','', 'Gateway label example:',data.label_example||'','', 'Exposure policy:',data.policy_url||'/policies'];result.textContent=lines.join('\n')}</script>`, inventoryPath, inventoryTitle, mode))
}
func requiredNodeTag(mode string) string {
	return "tag:tailscale-private-funnel"
}

func (c *Control) enrollmentCreate(w http.ResponseWriter, r *http.Request) {
	mode := chi.URLParam(r, "mode")
	if mode != "loadbalancer" && !store.IsGateway(mode) {
		http.Error(w, "invalid mode", 400)
		return
	}
	var input struct {
		Name     string `json:"name"`
		Hostname string `json:"hostname"`
		nodeDeployment
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			http.Error(w, "Invalid enrollment", 400)
			return
		}
	} else {
		input.DeploymentTarget = r.FormValue("deployment_target")
		input.InstallationName = r.FormValue("installation_name")
		input.HelmRelease = r.FormValue("helm_release")
		input.Name = r.FormValue("name")
		input.Hostname = r.FormValue("hostname")
		input.LoadBalancerHostname = r.FormValue("loadbalancer_hostname")
		input.CertEmail = r.FormValue("cert_email")
		input.LabelKeys = r.FormValue("label_keys")
		input.DenyNetworks = r.FormValue("deny_networks")
	}
	useHostDefault := strings.TrimSpace(input.Hostname) == ""
	if strings.TrimSpace(input.Hostname) == "" {
		input.Hostname = mode + "-node"
	}
	var defaults map[string]any
	if err := c.Store.GetJSON("tailnet", &defaults); err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Unable to read enrollment defaults", 503)
		return
	}
	if store.IsGateway(mode) && input.LoadBalancerHostname != "" {
		http.Error(w, "gateway does not accept load-balancer deployment fields", 400)
		return
	}
	if strings.TrimSpace(input.Name) == "" {
		input.Name = input.Hostname
	}
	name, nameErr := store.ValidateName(input.Name)
	if nameErr != nil {
		http.Error(w, nameErr.Error(), 400)
		return
	}
	if err := validateNodeDeployment(input.nodeDeployment, mode); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	version := stringValue(defaults, "release_version", "")
	if version == "" {
		version = releaseTagFromImage(stringValue(defaults, "image", ""))
		if !releasePattern.MatchString(version) {
			version = defaultRelease
		}
	}
	if err := c.checkRelease(r.Context(), version); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	e, code, err := c.Store.CreateEnrollment(mode, strings.TrimSpace(input.Hostname))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err = c.Store.RenameResource("enrollment", e.ID, name); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	e.Name = name
	_ = c.Store.Audit("enrollment.create:"+e.ID, identityFromRequest(r))
	var settings struct {
		Tailnet          string `json:"name"`
		Image            string `json:"image"`
		ManagementURL    string `json:"management_url"`
		CertEmail        string `json:"cert_email"`
		LoadBalancerHost string `json:"loadbalancer_hostname"`
		LabelKeys        string `json:"label_keys"`
		DenyNetworks     string `json:"deny_networks"`
	}
	_ = c.Store.GetJSON("tailnet", &settings)
	if settings.Image == "" {
		settings.Image = "ghcr.io/tailscale-x/tailscale-private-funnel:dev"
	}
	releaseVersion := version
	settings.Image = "ghcr.io/tailscale-x/tailscale-private-funnel:" + version
	settings.LoadBalancerHost = input.LoadBalancerHostname
	settings.CertEmail = input.CertEmail
	settings.LabelKeys = input.LabelKeys
	settings.DenyNetworks = input.DenyNetworks
	installCommand := fmt.Sprintf("curl -fsSL %s | sh -s -- --version %s", shellQuote("https://raw.githubusercontent.com/tailscale-x/tailscale-private-funnel/"+releaseVersion+"/scripts/install.sh"), shellQuote(releaseVersion))
	if strings.HasPrefix(c.BuildVersion, "dev-") {
		settings.Image = "ghcr.io/tailscale-x/tailscale-private-funnel:" + c.BuildVersion
		helper, e := Assets.ReadFile("assets/install-oci.py")
		if e != nil {
			http.Error(w, "Development installer unavailable", 500)
			return
		}
		installCommand = "command -v python3 >/dev/null || { echo 'Development binary installation requires Python 3' >&2; exit 1; }\npython3 - " + shellQuote(settings.Image) + " <<'FUNNEL_NATIVE_INSTALL'\n" + string(helper) + "\nFUNNEL_NATIVE_INSTALL"
	}

	managementURL := settings.ManagementURL
	if managementURL == "" {
		_ = c.Store.RevokeEnrollment(e.ID)
		http.Error(w, "configure a tailnet management URL in Tailscale settings first", http.StatusPreconditionRequired)
		return
	}
	controlPub, _, keyErr := c.Store.ConfigurationKey()
	if keyErr != nil {
		http.Error(w, keyErr.Error(), 500)
		return
	}
	clientID, idErr := c.Store.GetSecret("tailscale_oauth_client_id")
	clientSecret, secretErr := c.Store.GetSecret("tailscale_oauth_client_secret")
	var bootstrapKey string
	if idErr != nil || secretErr != nil || clientID == "" || clientSecret == "" {
		_ = c.Store.RevokeEnrollment(e.ID)
		http.Error(w, "configure Tailscale OAuth credentials first", http.StatusPreconditionRequired)
		return
	}
	bootstrapKey, keyErr = (c.tsClient(clientID, clientSecret, settings.Tailnet)).CreateAuthKey(r.Context(), mode, e.Hostname, []string{requiredNodeTag(mode)})
	if keyErr != nil {
		_ = c.Store.RevokeEnrollment(e.ID)
		http.Error(w, "create Tailscale enrollment key: "+keyErr.Error(), http.StatusBadGateway)
		return
	}
	deployArgs := fmt.Sprintf("%s join --management-url %s --hostname %s --enrollment-id %s --enrollment-code-file \"$code_file\" --advertise-tags %s --state-dir /var/lib/tailscale-private-funnel/%s/state --data-dir /var/lib/tailscale-private-funnel/%s/data", mode, shellQuote(managementURL), shellQuote(e.Hostname), shellQuote(e.ID), shellQuote(requiredNodeTag(mode)), mode, mode)
	if input.StateDir != "" {
		deployArgs = strings.Replace(deployArgs, "--state-dir /var/lib/tailscale-private-funnel/"+mode+"/state", "--state-dir "+shellQuote(input.StateDir), 1)
	}
	if input.DataDir != "" {
		deployArgs = strings.Replace(deployArgs, "--data-dir /var/lib/tailscale-private-funnel/"+mode+"/data", "--data-dir "+shellQuote(input.DataDir), 1)
	}
	if useHostDefault {
		deployArgs = strings.Replace(deployArgs, " --hostname "+shellQuote(e.Hostname), ` --hostname "$(hostname -s)-`+mode+`"`, 1)
	}
	deployArgs += " --control-key " + shellQuote(base64.RawStdEncoding.EncodeToString(controlPub))
	if settings.CertEmail != "" {
		deployArgs += " --cert-email " + shellQuote(settings.CertEmail)
	}
	if mode == "docker-gateway" {
	}
	if mode == "loadbalancer" && settings.LoadBalancerHost != "" {
		deployArgs += " --loadbalancer-hostname " + shellQuote(settings.LoadBalancerHost)
	}

	if settings.LabelKeys != "" {
		for _, key := range splitValues(settings.LabelKeys) {
			deployArgs += " --label-key " + shellQuote(key)
		}
	}
	if settings.DenyNetworks != "" {
		for _, pattern := range splitValues(settings.DenyNetworks) {
			deployArgs += " --deny-network " + shellQuote(pattern)
		}
	}
	if mode == "k8s-gateway" || mode == "loadbalancer" && input.DeploymentTarget == "kubernetes" {
		if input.HelmRelease == "" {
			sum := sha256.Sum256([]byte(e.ID))
			input.HelmRelease = fmt.Sprintf("%s-%x", mode, sum[:6])
		}
		input.nodeDeployment = resolveKubernetesDeployment(input.nodeDeployment)
		if mode == "loadbalancer" {
			input.AddressSource = "kubernetes"
			input.PublicService = input.HelmRelease + "-public"
		}
	}
	if err := c.Store.SetJSON("node_deployment:"+e.ID, input.nodeDeployment); err != nil {
		_ = c.Store.RevokeEnrollment(e.ID)
		http.Error(w, "Unable to save deployment choices", 500)
		return
	}
	capabilityCheck := ""
	deploymentScript := strings.Join([]string{
		"#!/bin/sh", "set -eu", "", installCommand,
		"binary=\"${XDG_BIN_HOME:-$HOME/.local/bin}/tailscale-private-funnel\"", capabilityCheck,
		"code_file=$(mktemp \"${TMPDIR:-/tmp}/private-funnel-enrollment.XXXXXX\")",
		"trap 'rm -f \"$code_file\"' EXIT HUP INT TERM",
		"umask 077", "printf '%s\\n' " + shellQuote(code) + " >\"$code_file\"",
		"TS_AUTHKEY=" + shellQuote(bootstrapKey) + " sudo --preserve-env=TS_AUTHKEY \"$binary\" " + deployArgs,
		"echo 'Deployment complete. Check with: tailscale-private-funnel " + mode + " status'",
	}, "\n")
	instructions := []string{
		"Copy the native deployment script to the target Linux host and run it once. It downloads and verifies the release binary, enrolls the node, installs a system-level systemd service, and starts it.",
		"The one-use Tailscale bootstrap key is held only in this shell environment; it is never written to the service unit or configuration. If this command is lost or the key expires, revoke this enrollment and create a new one.",
		"Keep the state and data directories persistent so the Tailscale identity and certificates survive replacement.",
	}
	if mode == "loadbalancer" {
		instructions = append(instructions, "Publish TCP ports 80 and 443 only on this loadbalancer. Control reconciles its owned A record from the enrolled Tailscale device endpoint.")
	} else {
		instructions = append(instructions, "Docker discovery requires host Docker socket permissions. The gateway validates selected user-defined application networks and creates isolated TCP relay containers. Relays have no Docker socket, Tailscale identity, or credentials.")
		instructions = append(instructions, "Every public hostname must match an enabled exposure rule and a managed DNS zone before control provisions routing and owned DNS.")
	}
	if mode == "k8s-gateway" {
		deploymentScript = helmEnrollmentScript(e.ID, code, bootstrapKey, base64.RawStdEncoding.EncodeToString(controlPub), managementURL, e.Hostname, settings.Image, input.nodeDeployment)
		instructions = []string{"Install the pinned Gateway API Standard CRDs first. Create a short-lived bootstrap Secret using the script, then install the matching version-pinned OCI Helm chart. The Secret is referenced by name, never stored in Helm values. Delete it once the controller joins. Keep the persistent volume for reboot-safe identity."}
	} else if mode == "loadbalancer" && input.DeploymentTarget == "kubernetes" {
		deploymentScript = loadBalancerHelmScript(e.ID, code, bootstrapKey, base64.RawStdEncoding.EncodeToString(controlPub), managementURL, e.Hostname, settings.Image, input.nodeDeployment)
		instructions = []string{"Install the OCI Helm chart using the generated short-lived Secret. Keep the PVC for identity and certificate persistence.", "The cluster's existing LoadBalancer implementation must allocate a routable address. Control uses signed Service-status observations, never the Pod's egress address.", "Delete the bootstrap Secret after enrollment. Additional load balancers use separate releases and PVCs in the same installation namespace."}
	} else if mode == "tailnet-gateway" {
		instructions = []string{"Run the native systemd installer; Docker is not required.", "Assign any additional backend-access tags and grants in Tailscale. Then choose this gateway in Expose a tailnet machine and run a connectivity test.", "Keep state and data persistent. Status diagnoses the node; reset and control revocation are explicit."}
	}
	labelExample := ""
	if mode == "docker-gateway" {
		labelExample = "tailscale-private-funnel.expose=8080:app.dev.example.com:http"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"enrollment": e, "code": code, "deployment_script": deploymentScript, "image": settings.Image, "instructions": instructions, "policy_url": "/policies", "label_example": labelExample, "backend_example": selectedGatewayExample(mode, input.nodeDeployment)})
}

func releaseTagFromImage(image string) string {
	const fallback = "dev"
	image = strings.TrimSpace(strings.SplitN(image, "@", 2)[0])
	colon := strings.LastIndexByte(image, ':')
	if colon < 0 || colon+1 >= len(image) {
		return fallback
	}
	tag := image[colon+1:]
	if len(tag) < 2 || tag[0] != 'v' {
		return fallback
	}
	for _, c := range tag[1:] {
		if (c < '0' || c > '9') && c != '.' {
			return fallback
		}
	}
	return tag
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (c *Control) enrollmentList(w http.ResponseWriter, r *http.Request) {
	mode := chi.URLParam(r, "mode")
	items, err := c.Store.Enrollments(mode)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(items)
}

func (c *Control) enrollmentRevoke(w http.ResponseWriter, r *http.Request) {
	if err := c.Store.RevokeEnrollment(chi.URLParam(r, "id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("enrollment.revoke:"+chi.URLParam(r, "id"), identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) enrollmentSigningKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	code := r.URL.Query().Get("code")
	var input struct {
		Code      string `json:"code"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err == nil {
		if code == "" {
			code = input.Code
		}
		if input.PublicKey != "" {
			input.PublicKey = strings.TrimSpace(input.PublicKey)
		}
	}
	_, err := c.Store.RedeemEnrollment(id, code)
	if err != nil {
		http.Error(w, "invalid enrollment", http.StatusForbidden)
		return
	}
	decodedKey, keyErr := base64.RawStdEncoding.DecodeString(input.PublicKey)
	if keyErr != nil || len(decodedKey) != ed25519.PublicKeySize {
		http.Error(w, "invalid Ed25519 key", 400)
		return
	}
	p := principal(r)
	if p.NodeID == "" {
		http.Error(w, "verified tailnet identity required", 403)
		return
	}
	// The revocable enrollment code proves possession of the intended
	// bootstrap command; the stable Tailscale identity is the binding stored
	// below. Hostnames can be suffixed by Tailscale when a name is already in
	// use, so they are not a safe identity key.
	if input.PublicKey == "" {
		http.Error(w, "public_key is required", 400)
		return
	}
	if err := c.Store.BindEnrollment(id, code, input.PublicKey, p.NodeID, p.Hostname, p.Addresses); err != nil {
		http.Error(w, "enrollment already consumed or invalid", http.StatusConflict)
		return
	}

	_ = c.Store.Audit("enrollment.bind-key:"+id, identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) nodeList(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "gateways"
	}
	if mode != "loadbalancer" && mode != "gateways" && !store.IsGateway(mode) {
		http.Error(w, "invalid mode", 400)
		return
	}
	nodes, err := c.Store.Nodes(mode)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(nodes)
}

func (c *Control) nodeRevoke(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := c.Store.RevokeNode(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("node.revoke:"+id, identityFromRequest(r))
	node, _ := c.Store.Node(id)
	err := c.cleanupRevokedNode(r.Context(), node)
	writeJSON(w, map[string]any{"revoked": true, "pending": err != nil, "message": fmt.Sprint(err)})
}
func render(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.Shell(title, body).Render(context.Background(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func recordViews(records []libdns.Record) []map[string]any {
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		rr := record.RR()
		out = append(out, map[string]any{"name": rr.Name, "type": rr.Type, "data": rr.Data})
	}
	return out
}
