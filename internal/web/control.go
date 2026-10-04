package web

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"github.com/tailscale-x/tailscale-private-funnel/internal/web/ui"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Control struct {
	Store           *store.Store
	TailscaleClient *tailscale.Client
	syncMu          sync.Mutex
	accessMu        sync.Mutex
	ReportTTL       time.Duration
}

type Principal struct {
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
// by both UI authorization and ingress exposure policy. Groups are supplied by
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

func roleConfigured(roles map[string][]string) bool {
	for _, values := range roles {
		if len(values) > 0 {
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
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return "viewer"
	}
	path := req.URL.Path
	if strings.HasPrefix(path, "/api/sync") || strings.HasPrefix(path, "/api/exposures/sync") || path == "/operations/sync" || path == "/api/reports/expire" {
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
	r.Post("/settings", c.saveSettings)
	r.Post("/api/settings/bootstrap", c.saveBootstrap)
	r.Post("/api/settings/deployment", c.saveDeployment)
	r.Get("/providers", c.providers)
	r.Get("/api/providers", c.providerList)
	r.Post("/api/providers", c.providerSave)
	r.Post("/api/providers/{id}/test", c.providerTest)
	r.Post("/api/providers/{id}/enable", c.providerEnable)
	r.Delete("/api/providers/{id}", c.providerDelete)
	r.Get("/zones", c.zones)
	r.Get("/applications", c.applications)
	r.Get("/api/zones", c.zoneList)
	r.Post("/api/zones", c.zoneSave)
	r.Post("/api/sync", c.sync)
	r.Post("/api/exposures/sync", c.exposureSync)
	r.Get("/operations", c.operations)
	r.Post("/operations/sync", c.operationsSync)
	r.Get("/access", c.accessPage)
	r.Get("/api/access", c.accessGet)
	r.Post("/api/access", c.accessSave)
	r.Get("/policies", c.policies)
	r.Get("/api/policies", c.policyGet)
	r.Post("/api/policies", c.policySave)
	r.Post("/api/policies/rule", c.policyRuleSave)
	r.Post("/api/policies/test", c.policyTest)
	r.Get("/api/reports", c.reports)
	r.Post("/api/reports", c.saveReport)
	r.Get("/api/node/config", c.nodeConfig)
	r.Post("/api/node/config/ack", c.nodeConfigAck)
	r.Post("/api/node/health", c.nodeHealth)
	r.Post("/api/node/shutdown", c.nodeShutdown)
	r.Get("/api/applications", c.applicationList)
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
	r.Get("/ingresses", c.nodes)
	r.Get("/exposures", c.exposures)
	r.Get("/api/nodes/{mode}", c.nodeList)
	r.Post("/api/nodes/{id}/revoke", c.nodeRevoke)
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
	ingresses, _ := c.Store.Nodes("ingress")
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
		nextTitle, nextCopy, nextHref = "Add an exposure policy", "Policies keep ingress hostnames scoped to the right node and domain.", "/policies"
	}
	if len(ingresses) > 0 {
		nextTitle, nextCopy, nextHref = "Review ingress exposures", "Inspect the latest signed reports and publish only approved routes.", "/exposures"
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
	ingressLabel, ingressTone := "No nodes", "neutral"
	if len(ingresses) > 0 {
		ingressLabel, ingressTone = "Awaiting reports", "warn"
		for _, node := range ingresses {
			if value, ok := node["last_report"].(int64); ok && value > 0 {
				ingressLabel, ingressTone = "Reporting", "ok"
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
	for _, node := range append(loadbalancers, ingresses...) {
		if value, ok := node["last_report"].(int64); ok && value > latestReport {
			latestReport = value
		}
	}
	controlLabel, controlTone := "Awaiting reports", "warn"
	if latestReport > 0 {
		controlLabel, controlTone = "Operational", "ok"
	}
	var b strings.Builder
	b.WriteString(pageIntro("Control plane", "Dashboard", "A calm view of your private edge. Follow the topology from control to loadbalancer, ingress, and the services you have approved."))
	b.WriteString(`<div class="pf-stat-grid">`)
	fmt.Fprintf(&b, `<a class="pf-stat" href="/loadbalancers"><span class="pf-stat-top"><span class="pf-stat-label">Load balancers</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">public edge nodes</span></a>`, pill(loadbalancerLabel, loadbalancerTone), len(loadbalancers))
	fmt.Fprintf(&b, `<a class="pf-stat" href="/ingresses"><span class="pf-stat-top"><span class="pf-stat-label">Ingress nodes</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">reporting nodes</span></a>`, pill(ingressLabel, ingressTone), len(ingresses))
	fmt.Fprintf(&b, `<a class="pf-stat" href="/exposures"><span class="pf-stat-top"><span class="pf-stat-label">Exposures</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">policy-scoped routes</span></a>`, pill("Owned", "ok"), exposureCount)
	fmt.Fprintf(&b, `<a class="pf-stat" href="/providers"><span class="pf-stat-top"><span class="pf-stat-label">DNS providers</span>%s</span><strong class="pf-stat-value">%d</strong><span class="pf-stat-note">%d managed zones</span></a>`, pill(providerLabel, providerTone), len(providers), len(zones))
	b.WriteString(`</div><div class="pf-dashboard-grid"><section class="pf-surface pf-topology"><div class="pf-section-heading"><div><p class="pf-kicker">Live topology</p><h2>How traffic moves</h2></div><a class="pf-text-link" href="/exposures">View exposures <span aria-hidden="true">→</span></a></div><div class="pf-topology-flow"><div class="pf-topology-node"><img src="/assets/icons/shield-check.svg" alt="" class="pf-icon"><strong>Control plane</strong><small>policy + DNS ownership</small><span class="pf-node-state">` + safeText(tailnet.Name) + `</span></div><span class="pf-topology-connector" aria-hidden="true">→</span><div class="pf-topology-node"><img src="/assets/icons/server-2.svg" alt="" class="pf-icon"><strong>Load balancers</strong><small>public TLS edge</small><span class="pf-node-state">` + fmt.Sprintf("%d nodes", len(loadbalancers)) + `</span></div><span class="pf-topology-connector" aria-hidden="true">→</span><div class="pf-topology-node"><img src="/assets/icons/route.svg" alt="" class="pf-icon"><strong>Ingress nodes</strong><small>tailnet backends</small><span class="pf-node-state">` + fmt.Sprintf("%d reporting", len(ingresses)) + `</span></div><span class="pf-topology-connector" aria-hidden="true">→</span><div class="pf-topology-node"><img src="/assets/icons/world.svg" alt="" class="pf-icon"><strong>Exposures</strong><small>approved hostnames</small><span class="pf-node-state">` + fmt.Sprintf("%d active", exposureCount) + `</span></div></div></section><aside class="pf-surface pf-next-step"><p class="pf-kicker">Next step</p><h2>` + safeText(nextTitle) + `</h2><p>` + safeText(nextCopy) + `</p><a class="pf-button" href="` + safeText(nextHref) + `">Open next step <span aria-hidden="true">→</span></a><div class="pf-next-meta"><span class="pf-status-dot pf-status-ok"></span><span>Changes stay within your ownership ledger.</span></div></aside></div>`)
	b.WriteString(`<div class="pf-dashboard-lower"><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Recent activity</p><h2>Audit events</h2></div><a class="pf-text-link" href="/api/audit">View all</a></div>`)
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
func (c *Control) settings(w http.ResponseWriter, _ *http.Request) {
	var current map[string]any
	_ = c.Store.GetJSON("tailnet", &current)
	value := func(key string) string { return safeText(stringValue(current, key, "")) }
	render(w, "Tailscale", pageIntro("Access", "Tailscale settings", "Connect the control node to your tailnet and choose the deployment values used by generated node deployment scripts.")+`<div class="pf-alert pf-alert-info"><strong>Tailnet-only management.</strong> OAuth secrets are stored encrypted and are never returned by the UI or included in installer commands.</div><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Identity and enrollment</p><h2>Tailnet connection</h2></div><a class="pf-text-link" href="/access">Configure access roles <span aria-hidden="true">→</span></a></div><form method="post" class="pf-form-grid" hx-post="/settings" hx-target="#result"><label class="pf-field"><span class="pf-label">Tailnet</span><input class="pf-input" required name="tailnet" value="`+value("name")+`" placeholder="example.ts.net"></label><label class="pf-field"><span class="pf-label">Role tags</span><input class="pf-input font-mono" value="tag:tailscale-private-funnel" readonly><small class="pf-help">All modes use this single OAuth-authorized tag. Mode and permissions are tracked by the control plane.</small></label><label class="pf-field"><span class="pf-label">Management URL</span><input class="pf-input" name="management_url" value="`+value("management_url")+`" placeholder="http://100.x.y.z:8080"></label><label class="pf-field"><span class="pf-label">OAuth client ID</span><input class="pf-input" name="client_id" placeholder="stored; enter only to replace"></label><label class="pf-field"><span class="pf-label">OAuth client secret</span><input class="pf-input" type="password" name="client_secret" placeholder="stored; enter only to replace"></label><div class="pf-form-actions"><button class="pf-button" type="submit">Save tailnet settings <span aria-hidden="true">→</span></button><span id="result" class="pf-inline-result" role="status"></span></div></form></section><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Deployment defaults</p><h2>Generated command values</h2></div></div><form method="post" class="pf-form-grid" hx-post="/settings" hx-target="#result"><label class="pf-field"><span class="pf-label">Release image</span><input class="pf-input font-mono" name="image" value="`+value("image")+`" placeholder="ghcr.io/tailscale-x/tailscale-private-funnel:vX.Y.Z"><small class="pf-help">Use an immutable release image tag for the runtime package. Generated deployment scripts pin the matching binary release.</small></label><label class="pf-field"><span class="pf-label">LoadBalancer hostname</span><input class="pf-input" name="loadbalancer_hostname" value="`+value("loadbalancer_hostname")+`" placeholder="loadbalancer.example.com"></label><label class="pf-field"><span class="pf-label">LoadBalancer public IPv4</span><input class="pf-input font-mono" name="loadbalancer_public_ipv4" value="`+value("loadbalancer_public_ipv4")+`" placeholder="198.51.100.10"><small class="pf-help">Optional. The loadbalancer reports this endpoint for the owned A record. Leave blank to retain the last owned record while the endpoint is unknown.</small></label><label class="pf-field"><span class="pf-label">Certificate email</span><input class="pf-input" type="email" name="cert_email" value="`+value("cert_email")+`" placeholder="ops@example.com"></label><label class="pf-field"><span class="pf-label">Exposure label aliases</span><input class="pf-input font-mono" name="label_keys" value="`+value("label_keys")+`" placeholder="company.example/expose"></label><label class="pf-field"><span class="pf-label">Local Docker deny patterns</span><input class="pf-input font-mono" name="deny_networks" value="`+value("deny_networks")+`" placeholder="prod-*,secret"></label><div class="pf-form-actions"><button class="pf-secondary-button" type="submit">Save deployment values</button></div></form></section>`)
}
func (c *Control) saveSettings(w http.ResponseWriter, r *http.Request) {
	var admins []string
	for _, admin := range strings.Split(r.FormValue("admins"), ",") {
		if admin = strings.TrimSpace(admin); admin != "" {
			admins = append(admins, admin)
		}
	}
	roles := map[string][]string{}
	for _, role := range []string{"admin", "operator", "viewer"} {
		for _, selector := range strings.Split(r.FormValue("role_"+role), ",") {
			if selector = strings.TrimSpace(selector); selector != "" {
				roles[role] = append(roles[role], selector)
			}
		}
	}
	settings := map[string]any{}
	_ = c.Store.GetJSON("tailnet", &settings)
	for key, value := range map[string]string{"name": r.FormValue("tailnet"), "management_url": r.FormValue("management_url"), "image": r.FormValue("image"), "bootstrap_url": r.FormValue("bootstrap_url"), "loadbalancer_hostname": r.FormValue("loadbalancer_hostname"), "loadbalancer_public_ipv4": r.FormValue("loadbalancer_public_ipv4"), "cert_email": r.FormValue("cert_email"), "label_keys": r.FormValue("label_keys"), "deny_networks": r.FormValue("deny_networks")} {
		if strings.TrimSpace(value) != "" {
			settings[key] = strings.TrimSpace(value)
		}
	}
	if len(admins) > 0 {
		settings["admins"] = admins
	}
	if len(roles) > 0 {
		settings["roles"] = roles
	}
	if err := c.Store.SetJSON("tailnet", settings); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// The settings page is split into identity and deployment forms. Preserve
	// existing encrypted OAuth credentials when the deployment form is saved.
	for key, value := range map[string]string{
		"tailscale_oauth_client_id":     r.FormValue("client_id"),
		"tailscale_oauth_client_secret": r.FormValue("client_secret"),
	} {
		if value = strings.TrimSpace(value); value == "" {
			continue
		}
		if err := c.Store.SetSecret(key, value); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		_, _ = w.Write([]byte("Saved."))
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
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

func (c *Control) saveDeployment(w http.ResponseWriter, r *http.Request) {
	var input map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "invalid deployment settings", http.StatusBadRequest)
		return
	}
	var settings map[string]any
	if err := c.Store.GetJSON("tailnet", &settings); err != nil {
		settings = map[string]any{}
	}
	for _, key := range []string{"management_url", "image", "cert_email", "loadbalancer_hostname", "label_keys", "deny_networks"} {
		if value, ok := input[key]; ok {
			settings[key] = strings.TrimSpace(value)
		}
	}
	if err := c.Store.SetJSON("tailnet", settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("settings.deployment", identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}
func (c *Control) providers(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	configured, _ := c.Store.Providers()
	b.WriteString(pageIntro("Infrastructure", "DNS providers", "Connect a supported libdns provider. Credentials are encrypted in SQLite and never rendered back to this page."))
	b.WriteString(`<section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Add provider</p><h2>Provider connection</h2></div><span class="pf-pill pf-pill-neutral">Cloudflare · GoDaddy · Route 53</span></div><form id="provider-form" class="pf-form-grid" hx-post="/api/providers" hx-target="#provider-result"><label class="pf-field"><span class="pf-label">Connection ID</span><input class="pf-input" required name="id" placeholder="cloudflare-dev"><small class="pf-help">A stable name used by the ownership ledger.</small></label><label class="pf-field"><span class="pf-label">Provider</span><select class="pf-input" name="provider">`)
	for _, p := range dns.Catalog {
		fmt.Fprintf(&b, `<option value="%s">%s</option>`, template.HTMLEscapeString(p.ID), template.HTMLEscapeString(p.Name))
	}
	b.WriteString(`</select></label><label class="pf-field"><span class="pf-label">Managed zone</span><input class="pf-input" required name="zone" placeholder="example.com"></label><label class="pf-field"><span class="pf-label">Cloudflare account ID</span><input class="pf-input" name="account_id" placeholder="optional account ID"></label><label class="pf-field pf-field-wide"><span class="pf-label">Credentials JSON</span><textarea class="pf-input" required name="config" rows="4" placeholder="{&quot;api_token&quot;:&quot;...&quot;}"></textarea><small class="pf-help">Use a scoped token. Existing secrets are write-only.</small></label><div class="pf-form-actions"><button class="pf-button" type="submit">Save provider <span aria-hidden="true">→</span></button><span id="provider-result" class="pf-inline-result" role="status"></span></div></form></section>`)
	b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Configured connections</p><h2>Provider health</h2></div><a class="pf-text-link" href="/api/providers">JSON</a></div>`)
	if len(configured) == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No providers configured</strong><span>Add Cloudflare to discover and reconcile a zone.</span></div>`)
	} else {
		b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Provider</th><th>Zone</th><th>Account</th><th>State</th><th>Capabilities</th><th>Last test</th><th>Actions</th></tr></thead><tbody>`)
		for _, p := range configured {
			state := "Disabled"
			tone := "neutral"
			if p.Enabled {
				state, tone = "Enabled", "ok"
			}
			tested := "Not tested"
			if p.TestedAt > 0 {
				tested = unixLabel(p.TestedAt)
			}
			action := `<div class="pf-inline-actions"><form method="post" action="/api/providers/` + safeText(p.ID) + `/test"><button class="pf-link-button" type="submit">Test</button></form><form method="post" action="/api/providers/` + safeText(p.ID) + `/enable"><input type="hidden" name="enabled" value="` + map[bool]string{true: "false", false: "true"}[p.Enabled] + `"><button class="pf-link-button" type="submit">` + map[bool]string{true: "Disable", false: "Enable"}[p.Enabled] + `</button></form><button class="pf-link-button pf-link-danger" type="button" onclick="if(confirm('Delete this provider and its configuration?')) fetch('/api/providers/` + safeText(p.ID) + `',{method:'DELETE'}).then(()=>location.reload())">Delete</button></div>`
			fmt.Fprintf(&b, `<tr><td data-label="Provider"><strong>%s</strong><small class="pf-muted">%s</small></td><td data-label="Zone"><code>%s</code></td><td data-label="Account"><code>%s</code></td><td data-label="State">%s</td><td data-label="Capabilities"><span class="pf-muted">%s</span></td><td data-label="Last test"><span class="pf-muted">%s</span></td><td data-label="Actions">%s</td></tr>`, safeText(p.ID), safeText(p.Provider), safeText(p.Zone), safeText(p.AccountID), pill(state, tone), safeText(strings.Join(p.Capabilities, ", ")), safeText(tested), action)
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</section>`)
	render(w, "DNS Providers", b.String())
}
func (c *Control) zones(w http.ResponseWriter, _ *http.Request) {
	zones, _ := c.Store.Zones()
	var b strings.Builder
	b.WriteString(pageIntro("Infrastructure", "Managed zones", "Assign an ownership ID before any record is changed. Discovery is read-only; reconciliation stays behind Operations."))
	b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Ownership ledger</p><h2>Zones in scope</h2></div><a class="pf-text-link" href="/api/zones">Discover zones <span aria-hidden="true">→</span></a></div>`)
	if len(zones) == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No managed zones</strong><span>Save a provider, discover its zones, then assign one to this control plane.</span></div>`)
	} else {
		b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Provider</th><th>Zone</th><th>Ownership ID</th><th>State</th></tr></thead><tbody>`)
		for _, z := range zones {
			state, tone := "Disabled", "neutral"
			if z.Enabled {
				state, tone = "Ready", "ok"
			}
			fmt.Fprintf(&b, `<tr><td data-label="Provider"><strong>%s</strong></td><td data-label="Zone"><code>%s</code></td><td data-label="Ownership ID"><code>%s</code></td><td data-label="State">%s</td></tr>`, safeText(z.ProviderID), safeText(z.Zone), safeText(z.OwnerID), pill(state, tone))
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</section><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Assign ownership</p><h2>Register a zone</h2></div></div><form class="pf-form-grid" method="post" action="/api/zones"><label class="pf-field"><span class="pf-label">Provider ID</span><input class="pf-input" name="provider_id" required placeholder="cloudflare-dev"></label><label class="pf-field"><span class="pf-label">Zone</span><input class="pf-input" name="zone" required placeholder="example.com"></label><label class="pf-field"><span class="pf-label">Ownership ID</span><input class="pf-input" name="owner_id" required placeholder="private-funnel-prod"></label><div class="pf-form-actions"><button class="pf-button" type="submit">Save ownership</button></div></form></section>`)
	render(w, "Zones", b.String())
}

func (c *Control) providerList(w http.ResponseWriter, _ *http.Request) {
	providers, err := c.Store.Providers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(providers)
}

type providerInput struct {
	ID        string            `json:"id"`
	Provider  string            `json:"provider"`
	Zone      string            `json:"zone"`
	Config    map[string]string `json:"config"`
	AccountID string            `json:"account_id"`
	Enabled   *bool             `json:"enabled"`
}

func (c *Control) providerSave(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		in.ID, in.Provider, in.Zone = strings.TrimSpace(r.FormValue("id")), strings.TrimSpace(r.FormValue("provider")), strings.TrimSpace(r.FormValue("zone"))
		if err := json.Unmarshal([]byte(r.FormValue("config")), &in.Config); err != nil {
			http.Error(w, "config must be a JSON object", http.StatusBadRequest)
			return
		}
		if accountID := strings.TrimSpace(r.FormValue("account_id")); accountID != "" {
			if in.Config == nil {
				in.Config = map[string]string{}
			}
			in.Config["account_id"] = accountID
		}
	} else if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid provider", http.StatusBadRequest)
		return
	}
	if in.ID == "" || in.Provider == "" || in.Zone == "" || len(in.Config) == 0 {
		http.Error(w, "id, provider, zone, and config are required", http.StatusBadRequest)
		return
	}
	if _, ok := dns.Registry[in.Provider]; !ok {
		http.Error(w, "provider has no linked runtime adapter", http.StatusBadRequest)
		return
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if err := c.Store.SaveProvider(in.ID, in.Provider, in.Zone, in.Config, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("provider.save:"+in.ID, identityFromRequest(r))
	if r.Header.Get("HX-Request") == "true" {
		_, _ = w.Write([]byte("Provider saved."))
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (c *Control) providerTest(w http.ResponseWriter, r *http.Request) {
	p, config, err := c.Store.ProviderConfig(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "provider not found", http.StatusNotFound)
		return
	}
	factory := dns.Registry[p.Provider]
	if factory == nil {
		http.Error(w, "provider has no linked runtime adapter", http.StatusBadRequest)
		return
	}
	provider, err := factory(config)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	records, err := provider.List(r.Context(), p.Zone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	capabilities := provider.Capabilities()
	_ = c.Store.MarkProviderTested(p.ID, capabilities)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"provider": p.ID, "zone": p.Zone, "records": len(records), "capabilities": capabilities})
}

func (c *Control) providerDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.Store.DeleteProvider(chi.URLParam(r, "id")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("provider.delete:"+chi.URLParam(r, "id"), identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) providerEnable(w http.ResponseWriter, r *http.Request) {
	enabled := r.FormValue("enabled") != "false"
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var input struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err == nil && input.Enabled != nil {
			enabled = *input.Enabled
		}
	}
	if err := c.Store.SetProviderEnabled(chi.URLParam(r, "id"), enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("provider.enable:"+chi.URLParam(r, "id"), identityFromRequest(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"id": chi.URLParam(r, "id"), "enabled": enabled})
}

func (c *Control) zoneList(w http.ResponseWriter, r *http.Request) {
	providers, err := c.Store.Providers()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type zone struct {
		Provider string `json:"provider"`
		Zone     string `json:"zone"`
		Source   string `json:"source"`
	}
	var out []zone
	if managed, managedErr := c.Store.Zones(); managedErr == nil {
		for _, z := range managed {
			out = append(out, zone{Provider: z.ProviderID, Zone: z.Zone, Source: "managed"})
		}
	}
	for _, p := range providers {
		out = append(out, zone{Provider: p.ID, Zone: p.Zone, Source: "configured"})
		meta, config, configErr := c.Store.ProviderConfig(p.ID)
		if configErr != nil {
			continue
		}
		factory := dns.Registry[meta.Provider]
		if factory == nil {
			continue
		}
		provider, factoryErr := factory(config)
		if factoryErr != nil {
			continue
		}
		if lister, ok := provider.(interface {
			ListZones(context.Context) ([]libdns.Zone, error)
		}); ok {
			zones, listErr := lister.ListZones(r.Context())
			if listErr == nil {
				for _, z := range zones {
					out = append(out, zone{Provider: p.ID, Zone: z.Name, Source: "discovered"})
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (c *Control) zoneSave(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ProviderID string `json:"provider_id"`
		Zone       string `json:"zone"`
		OwnerID    string `json:"owner_id"`
		Enabled    *bool  `json:"enabled"`
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		input.ProviderID, input.Zone, input.OwnerID = strings.TrimSpace(r.FormValue("provider_id")), strings.TrimSpace(r.FormValue("zone")), strings.TrimSpace(r.FormValue("owner_id"))
	} else if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		http.Error(w, "invalid zone", http.StatusBadRequest)
		return
	}
	if input.ProviderID == "" || input.Zone == "" || input.OwnerID == "" {
		http.Error(w, "provider_id, zone, and owner_id are required", http.StatusBadRequest)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if err := c.Store.SaveZone(input.ProviderID, input.Zone, input.OwnerID, enabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("zone.save:"+input.ProviderID+":"+input.Zone, identityFromRequest(r))
	if r.Header.Get("HX-Request") == "true" {
		fmt.Fprint(w, "Zone ownership saved.")
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		http.Redirect(w, r, "/zones", http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

type syncRecord struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"`
	TTL  int    `json:"ttl"`
}

type syncInput struct {
	ProviderID    string       `json:"provider_id"`
	OwnerID       string       `json:"owner_id"`
	Records       []syncRecord `json:"records"`
	DryRun        bool         `json:"dry_run"`
	AllowUnowned  bool         `json:"allow_unowned"`
	ApprovalToken string       `json:"approval_token"`
}

func (c *Control) sync(w http.ResponseWriter, r *http.Request) {
	var in syncInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid sync request", http.StatusBadRequest)
		return
	}
	if in.ProviderID == "" || in.OwnerID == "" {
		http.Error(w, "provider_id and owner_id are required", http.StatusBadRequest)
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
	result, err := c.reconcile(r.Context(), in.ProviderID, in.OwnerID, desired, in.DryRun, in.AllowUnowned, in.ApprovalToken)
	if err != nil {
		http.Error(w, err.Error(), resultStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	result["dry_run"] = in.DryRun
	result["owner_id"] = in.OwnerID
	_ = json.NewEncoder(w).Encode(result)
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

func (c *Control) reconcile(ctx context.Context, providerID, ownerID string, desired []libdns.Record, dryRun, allowUnowned bool, approvalToken ...string) (map[string]any, error) {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	p, config, err := c.Store.ProviderConfig(providerID)
	if err != nil {
		return nil, fmt.Errorf("provider not found")
	}
	if !p.Enabled {
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
	zone := strings.ToLower(strings.TrimSuffix(p.Zone, "."))
	desired = dns.NormalizeRecords(desired, zone)
	for _, record := range desired {
		name := strings.ToLower(strings.TrimSuffix(record.RR().Name, "."))
		if name != zone && !strings.HasSuffix(name, "."+zone) {
			return nil, fmt.Errorf("record %s is outside zone %s", name, zone)
		}
	}
	allLedger, err := c.Store.ZoneLedger(p.Zone)
	if err != nil {
		return nil, err
	}
	existing, err := provider.List(ctx, p.Zone)
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
		token, tokenErr := c.approval(providerID, ownerID, desired, existing, allLedger, "")
		if tokenErr != nil {
			return nil, tokenErr
		}
		return map[string]any{"add": len(add), "remove": len(remove), "requires_approval": true, "approval_token": token, "existing_records": recordViews(existing), "desired_records": recordViews(desired)}, nil
	}
	if allowUnowned && !dryRun {
		token := ""
		if len(approvalToken) > 0 {
			token = approvalToken[0]
		}
		if token == "" {
			return nil, fmt.Errorf("overwrite approval is required; preview the specific changes first")
		}
		if _, approvalErr := c.approval(providerID, ownerID, desired, existing, allLedger, token); approvalErr != nil {
			return nil, approvalErr
		}
		add, remove, err = dns.OwnershipPlan(ownerID, allLedger, existing, desired, true)
	}
	if err != nil {
		return nil, err
	}
	if !dryRun {
		if err := provider.Apply(ctx, p.Zone, add, remove); err != nil {
			return nil, err
		}
		if allowUnowned {
			transferred := append(append(append([]libdns.Record{}, desired...), add...), remove...)
			if err := c.Store.TransferZoneLedgerOwnership(p.Zone, ownerID, transferred); err != nil {
				return nil, err
			}
		}
		if err := c.Store.ReplaceLedger(p.ID, p.Zone, ownerID, desired); err != nil {
			return nil, err
		}
		_ = c.Store.Audit("dns.reconcile:"+ownerID, "control")
		if allowUnowned {
			_ = c.Store.Audit("dns.explicit-overwrite:"+ownerID, "administrator")
		}
	}
	return map[string]any{"add": len(add), "remove": len(remove), "requires_approval": false}, nil
}

type exposureInput struct {
	Kind          string `json:"kind"`
	ProviderID    string `json:"provider_id"`
	OwnerID       string `json:"owner_id"`
	DryRun        bool   `json:"dry_run"`
	AllowUnowned  bool   `json:"allow_unowned"`
	ApprovalToken string `json:"approval_token"`
	dns.IngressExposure
	LoadBalancer dns.LoadBalancerEndpoint `json:"loadbalancer"`
}

func (c *Control) exposureSync(w http.ResponseWriter, r *http.Request) {
	var in exposureInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid exposure", 400)
		return
	}
	if in.ProviderID == "" || in.OwnerID == "" {
		http.Error(w, "provider_id and owner_id are required", 400)
		return
	}
	var records []libdns.Record
	var err error
	if in.Kind == "ingress" {
		records, err = dns.IngressRecords(in.IngressExposure)
		if err == nil {
			p := principal(r)
			ingressID := strings.TrimPrefix(in.OwnerID, "ingress:")
			if !c.exposureAllowed(in.IngressExposure, []string{ingressID, in.OwnerID, p.NodeID}, p.RoleSelectors()) {
				http.Error(w, "ingress exposure is not allowed by the control policy", http.StatusForbidden)
				return
			}
		}
	} else if in.Kind == "loadbalancer" {
		var record libdns.Record
		record, _, err = dns.LoadBalancerRecord(in.LoadBalancer)
		if err == nil {
			records = []libdns.Record{record}
		}
	} else {
		err = fmt.Errorf("kind must be loadbalancer or ingress")
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	result, err := c.reconcile(r.Context(), in.ProviderID, in.OwnerID, records, in.DryRun, in.AllowUnowned, in.ApprovalToken)
	if err != nil {
		http.Error(w, err.Error(), resultStatus(err))
		return
	}
	result["dry_run"] = in.DryRun
	result["owner_id"] = in.OwnerID
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (c *Control) operations(w http.ResponseWriter, _ *http.Request) {
	render(w, "Operations", pageIntro("Operations", "DNS operations", "Preview the exact desired state, separate owned records from unowned records, and explicitly approve any overwrite before applying.")+`<div class="pf-alert pf-alert-warn"><strong>Ownership guardrail.</strong> The control plane never changes a record outside this installation's ownership ledger unless an administrator approves the specific preview.</div><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Dry-run first</p><h2>Reconcile records</h2></div><a class="pf-text-link" href="/api/reports">View reports <span aria-hidden="true">→</span></a></div><label class="pf-field"><span class="pf-label">Sync request JSON</span><textarea class="pf-input font-mono" id="sync-input" rows="10">{"provider_id":"","owner_id":"","records":[],"dry_run":true}</textarea></label><div class="pf-form-actions"><button class="pf-button" id="preview" type="button">Preview changes <span aria-hidden="true">→</span></button><label class="pf-check-field"><input id="approve" type="checkbox"><span><strong>Approve unowned overwrite</strong><small class="pf-help">Only the records shown in the latest preview can be transferred.</small></span></label><button class="pf-secondary-button" id="apply" type="button" disabled>Apply approved preview</button></div><pre class="pf-command" id="sync-result" aria-live="polite"></pre></section>
<script>
const input=document.getElementById('sync-input'), result=document.getElementById('sync-result'), approve=document.getElementById('approve'), apply=document.getElementById('apply'); let token='', requiresApproval=false;
async function send(body){const r=await fetch('/api/sync',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify(body)});const t=await r.text();result.textContent=t;return {r,t}}
document.getElementById('preview').onclick=async()=>{try{const b=JSON.parse(input.value);b.dry_run=true;b.allow_unowned=false;const x=await send(b);if(x.r.ok){const j=JSON.parse(x.t);token=j.approval_token||'';requiresApproval=Boolean(j.requires_approval);apply.disabled=requiresApproval&&!token;}}catch(e){result.textContent=e}}
apply.onclick=async()=>{if(requiresApproval&&(!approve.checked||!token))return;try{const b=JSON.parse(input.value);b.dry_run=false;b.allow_unowned=requiresApproval;b.approval_token=token;await send(b)}catch(e){result.textContent=e}}
</script>`)
}

func (c *Control) loadExposurePolicy() policy.Document {
	document := policy.Document{Rules: []policy.Rule{}}
	if err := c.Store.GetJSON("exposure_policy", &document); err != nil {
		return policy.Document{}
	}
	return document
}

func (c *Control) exposureAllowed(exposure dns.IngressExposure, ingressIDs, roles []string) bool {
	allowed, _ := c.loadExposurePolicy().Allows(exposure.PublicHostname, exposure.LoadBalancerHostname, ingressIDs, roles)
	return allowed
}

func (c *Control) policies(w http.ResponseWriter, _ *http.Request) {
	document := c.loadExposurePolicy()
	b, _ := json.MarshalIndent(document, "", "  ")
	initial := template.HTMLEscapeString(string(b))
	render(w, "Ingress exposure policy", pageIntro("Access", "Exposure policy", "Default deny keeps ingress labels from becoming public DNS. Add a narrow rule for the hostnames and Tailscale roles that are allowed to publish.")+`<div class="pf-alert pf-alert-info"><strong>Default deny is active.</strong> A report is accepted only when hostname, loadbalancer, and ingress identity match an enabled rule.</div><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Guided rule editor</p><h2>Add an authorization rule</h2></div><span class="pf-pill pf-pill-neutral">RE2 regex</span></div><form class="pf-form-grid" hx-post="/api/policies/rule" hx-target="#policy-result"><label class="pf-field"><span class="pf-label">Rule name</span><input class="pf-input" name="name" placeholder="Development services"></label><label class="pf-field"><span class="pf-label">Hostname regex</span><input class="pf-input font-mono" name="hostname_regex" required placeholder="[^.]+[.]dev[.]example[.]com"><small class="pf-help">The expression is anchored to the complete hostname.</small></label><label class="pf-field"><span class="pf-label">Ingress roles</span><input class="pf-input" name="roles" placeholder="tag:ingress-dev,group:platform"></label><label class="pf-field"><span class="pf-label">Ingress node IDs</span><input class="pf-input" name="ingress_ids" placeholder="ingress-node-id"></label><label class="pf-field"><span class="pf-label">LoadBalancer hostnames</span><input class="pf-input" name="loadbalancer_hostnames" placeholder="loadbalancer.example.com"></label><label class="pf-field pf-check-field"><input type="checkbox" name="enabled" checked><span><strong>Enabled</strong><small class="pf-help">Enable publication immediately after saving.</small></span></label><div class="pf-form-actions"><button class="pf-button" type="submit">Save rule <span aria-hidden="true">→</span></button><span id="policy-result" class="pf-inline-result" role="status"></span></div></form></section><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Current rules</p><h2>Policy coverage</h2></div><span class="pf-pill pf-pill-neutral">`+fmt.Sprintf("%d rules", len(document.Rules))+`</span></div>`+policyRuleTable(document)+`</section><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Policy test</p><h2>Check a route before deploying it</h2></div><span class="pf-pill pf-pill-warn">Denied by default</span></div><form class="pf-form-grid" hx-post="/api/policies/test" hx-target="#policy-test-result"><label class="pf-field"><span class="pf-label">Hostname</span><input class="pf-input font-mono" name="hostname" required placeholder="app.dev.example.com"></label><label class="pf-field"><span class="pf-label">LoadBalancer hostname</span><input class="pf-input" name="loadbalancer_hostname" required placeholder="loadbalancer.example.com"></label><label class="pf-field"><span class="pf-label">Ingress roles</span><input class="pf-input" name="roles" placeholder="tag:ingress-dev"></label><label class="pf-field"><span class="pf-label">Ingress node IDs</span><input class="pf-input" name="ingress_ids" placeholder="ingress-node-id"></label><div class="pf-form-actions"><button class="pf-secondary-button" type="submit">Test policy</button></div></form><pre class="pf-command" id="policy-test-result" aria-live="polite"></pre></section><section class="pf-surface pf-form-surface"><details><summary class="pf-summary">Advanced JSON import / export</summary><form method="post" action="/api/policies" hx-post="/api/policies" hx-target="#policy-json-result"><label class="pf-field"><span class="pf-label">Policy document</span><textarea class="pf-input font-mono" name="policy" rows="16" required>`+initial+`</textarea></label><button class="pf-secondary-button" type="submit">Validate JSON</button></form><pre id="policy-json-result"></pre></details></section>`)
}

func policyRuleTable(document policy.Document) string {
	if len(document.Rules) == 0 {
		return `<div class="pf-empty"><strong>No rules configured</strong><span>Automatic ingress reports stay denied until you add a matching rule.</span></div>`
	}
	var b strings.Builder
	b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Name</th><th>Hostname</th><th>Ingress selectors</th><th>Load balancers</th><th>State</th></tr></thead><tbody>`)
	for _, rule := range document.Rules {
		state, tone := "Disabled", "neutral"
		if rule.Enabled {
			state, tone = "Enabled", "ok"
		}
		selectors := append(append([]string{}, rule.Roles...), rule.IngressIDs...)
		loadbalancers := strings.Join(rule.LoadBalancerHostnames, ", ")
		fmt.Fprintf(&b, `<tr><td data-label="Name"><strong>%s</strong></td><td data-label="Hostname"><code>%s</code></td><td data-label="Ingress selectors"><span class="pf-muted">%s</span></td><td data-label="Load balancers"><span class="pf-muted">%s</span></td><td data-label="State">%s</td></tr>`, safeText(rule.Name), safeText(rule.HostnameRegex), safeText(strings.Join(selectors, ", ")), safeText(loadbalancers), pill(state, tone))
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

func (c *Control) policyRuleSave(w http.ResponseWriter, r *http.Request) {
	document := c.loadExposurePolicy()
	rule := policy.Rule{Name: strings.TrimSpace(r.FormValue("name")), HostnameRegex: strings.TrimSpace(r.FormValue("hostname_regex")), Roles: splitValues(r.FormValue("roles")), IngressIDs: splitValues(r.FormValue("ingress_ids")), LoadBalancerHostnames: splitValues(r.FormValue("loadbalancer_hostnames")), Enabled: r.FormValue("enabled") == "on" || r.FormValue("enabled") == "true"}
	document.Rules = append(document.Rules, rule)
	if err := document.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := c.Store.SetJSON("exposure_policy", document); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("exposure-policy.rule.save", identityFromRequest(r))
	if r.Header.Get("HX-Request") == "true" {
		fmt.Fprint(w, `<span class="pf-result-success">Rule saved. Reload the policy page to review coverage.</span>`)
		return
	}
	http.Redirect(w, r, "/policies", http.StatusSeeOther)
}

func (c *Control) policyTest(w http.ResponseWriter, r *http.Request) {
	hostname, loadbalancer := strings.TrimSpace(r.FormValue("hostname")), strings.TrimSpace(r.FormValue("loadbalancer_hostname"))
	allowed, matched := c.loadExposurePolicy().Allows(hostname, loadbalancer, splitValues(r.FormValue("ingress_ids")), splitValues(r.FormValue("roles")))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"allowed": allowed, "matched_rule": matched, "hostname": hostname})
}

func (c *Control) policyGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c.loadExposurePolicy())
}

func (c *Control) policySave(w http.ResponseWriter, r *http.Request) {
	var document policy.Document
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&document); err != nil {
			http.Error(w, "invalid policy JSON", http.StatusBadRequest)
			return
		}
	} else {
		rules := strings.TrimSpace(r.FormValue("policy"))
		if rules == "" {
			http.Error(w, "policy JSON is required", http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal([]byte(rules), &document); err != nil {
			http.Error(w, "invalid policy JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := document.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := c.Store.SetJSON("exposure_policy", document); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c.Store.Audit("exposure-policy.save", identityFromRequest(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(document)
}

func (c *Control) exposures(w http.ResponseWriter, _ *http.Request) {
	reports, err := c.Store.Reports()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString(pageIntro("Overview", "Exposures", "Signed ingress reports become DNS desired state only when the hostname, loadbalancer, and ingress identity pass policy."))
	b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Approved routes</p><h2>Public hostnames</h2></div><span class="pf-pill pf-pill-ok"><i class="pf-status-dot pf-status-ok"></i> Ownership scoped</span></div>`)
	rows := 0
	for nodeID, raw := range reports {
		var envelope report.Envelope
		if json.Unmarshal([]byte(raw), &envelope) != nil {
			continue
		}
		var payload struct {
			Exposures []dns.IngressExposure `json:"exposures"`
			ExpiresAt int64                 `json:"expires_at"`
		}
		if json.Unmarshal(envelope.Payload, &payload) != nil {
			continue
		}
		for _, exposure := range payload.Exposures {
			rows++
			state := pill("Backend unavailable", "error")
			if exposure.Reachable {
				state = pill("Allowed", "ok")
			}
			networks := strings.Join(exposure.Networks, ", ")
			if networks == "" {
				networks = "none reported"
			}
			fmt.Fprintf(&b, `<div class="pf-exposure-row"><div><strong class="font-mono">%s</strong><small class="pf-muted">loadbalancer · %s</small></div><div><span class="pf-muted">Backend</span><code>%s:%d</code><small class="pf-muted">networks · %s</small></div><div><span class="pf-muted">Ingress</span><code>%s</code></div><div>%s<small class="pf-muted">expires %s</small></div></div>`, safeText(exposure.PublicHostname), safeText(exposure.LoadBalancerHostname), safeText(exposure.BackendHostname), exposure.Port, safeText(networks), safeText(nodeID), state, safeText(unixLabel(payload.ExpiresAt)))
		}
	}
	if rows == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No active exposures</strong><span>Start a labeled ingress service covered by an enabled policy rule to see it here.</span><a class="pf-text-link" href="/policies">Review exposure policy <span aria-hidden="true">→</span></a></div>`)
	}
	b.WriteString(`</section><section class="pf-surface"><details><summary class="pf-summary">Advanced report payloads</summary><pre class="pf-command">` + template.HTMLEscapeString(func() string { raw, _ := json.MarshalIndent(reports, "", "  "); return string(raw) }()) + `</pre></details></section>`)
	render(w, "Exposures", b.String())
}

func (c *Control) reports(w http.ResponseWriter, _ *http.Request) {
	reports, err := c.Store.Reports()
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
	return node, nil
}
func applicationAllows(app store.ApplicationRecord, node store.NodeRecord) bool {
	return app.Enabled && !node.Revoked && node.Mode == "ingress" && (len(app.AllowedIngressNodes) == 0 || contains(app.AllowedIngressNodes, node.ID) || contains(app.AllowedIngressNodes, node.TailnetID))
}
func (c *Control) nodeConfig(w http.ResponseWriter, r *http.Request) {
	if n, err := c.callerNode(r, "loadbalancer"); err == nil && n.Mode == "loadbalancer" {
		c.loadBalancerConfig(w, r)
		return
	}
	node, err := c.callerNode(r, "ingress")
	id := node.ID
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var settings struct {
		LabelKeys    string `json:"label_keys"`
		LoadBalancer string `json:"loadbalancer_hostname"`
	}
	_ = c.Store.GetJSON("tailnet", &settings)
	filtered := make([]store.ApplicationRecord, 0)
	for _, app := range apps {
		if !applicationAllows(app, node) {
			continue
		}
		hosts := make([]string, 0)
		for _, host := range app.PublicHostnames {
			if ok, _ := c.loadExposurePolicy().Allows(host, settings.LoadBalancer, []string{id, node.TailnetID}, principal(r).RoleSelectors()); ok {
				hosts = append(hosts, host)
			}
		}
		app.PublicHostnames = hosts
		if len(hosts) > 0 {
			filtered = append(filtered, app)
		}
	}
	config := nodeconfig.Configuration{NodeID: id, Applications: filtered, LabelAliases: splitValues(settings.LabelKeys), LoadBalancerHostname: settings.LoadBalancer}
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
	health["received_at"] = time.Now().Unix()
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
	_ = c.Store.RevokeRelays(node.ID)
	_ = c.Store.DeleteReport(node.ID)
	if client, clientErr := c.tailscaleRuntimeClient(); clientErr == nil {
		if services, listErr := client.ListServices(r.Context()); listErr == nil {
			for _, service := range services {
				hosts, hostsErr := client.ListServiceHosts(r.Context(), service.Name)
				if hostsErr != nil {
					continue
				}
				for _, host := range hosts {
					if host.NodeID == node.TailnetID {
						_, _ = client.ApproveServiceDevice(r.Context(), service.Name, host.NodeID, false)
					}
				}
			}
		}
		if devices, listErr := client.ListDevices(r.Context()); listErr == nil {
			for _, device := range devices {
				if device.NodeID == node.TailnetID {
					if deleteErr := client.DeleteDevice(r.Context(), device.ID); deleteErr != nil {
						_ = c.Store.Audit("node.cleanup.error:"+node.ID, deleteErr.Error())
					}
				}
			}
		}
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
	PublicIPv4 []string `json:"public_ipv4"`
	ReceivedAt int64    `json:"received_at"`
}

func (c *Control) loadBalancerProvider(hostname string) (store.ProviderRecord, error) {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if hostname == "" {
		return store.ProviderRecord{}, fmt.Errorf("load balancer hostname is not configured")
	}
	zones, err := c.Store.Zones()
	if err != nil {
		return store.ProviderRecord{}, err
	}
	providers, err := c.Store.Providers()
	if err != nil {
		return store.ProviderRecord{}, err
	}
	byID := make(map[string]store.ProviderRecord, len(providers))
	for _, provider := range providers {
		if provider.Enabled {
			byID[provider.ID] = provider
		}
	}
	var selected store.ProviderRecord
	selectedZone := ""
	for _, zone := range zones {
		zoneName := strings.ToLower(strings.TrimSuffix(zone.Zone, "."))
		if !zone.Enabled || (hostname != zoneName && !strings.HasSuffix(hostname, "."+zoneName)) {
			continue
		}
		provider, ok := byID[zone.ProviderID]
		if !ok {
			continue
		}
		candidateZone := zoneName
		if selected.ID != "" && len(candidateZone) == len(selectedZone) {
			return store.ProviderRecord{}, fmt.Errorf("load balancer hostname matches multiple managed DNS zones")
		}
		if len(candidateZone) > len(selectedZone) {
			selected, selectedZone = provider, candidateZone
		}
	}
	if selected.ID == "" {
		return store.ProviderRecord{}, fmt.Errorf("no enabled managed DNS zone matches load balancer hostname %q", hostname)
	}
	return selected, nil
}

func (c *Control) reconcileLoadBalancerNode(ctx context.Context, node store.NodeRecord) error {
	var settings struct {
		Hostname   string `json:"loadbalancer_hostname"`
		Name       string `json:"name"`
		PublicIPv4 string `json:"loadbalancer_public_ipv4"`
	}
	if err := c.Store.GetJSON("tailnet", &settings); err != nil {
		return fmt.Errorf("load balancer settings are not configured")
	}
	provider, err := c.loadBalancerProvider(settings.Hostname)
	if err != nil {
		return err
	}
	ownerID := "loadbalancer:" + node.ID
	var health loadBalancerHealth
	healthErr := c.Store.GetJSON("node_health:"+node.ID, &health)
	freshness := c.ReportTTL
	if freshness <= 0 {
		freshness = 5 * time.Minute
	}
	stale := node.Revoked || healthErr != nil || health.ReceivedAt <= 0 || time.Since(time.Unix(health.ReceivedAt, 0)) > freshness
	if stale {
		_, err = c.reconcile(ctx, provider.ID, ownerID, nil, false, false)
		return err
	}
	if settings.PublicIPv4 != "" {
		health.PublicIPv4 = splitValues(settings.PublicIPv4)
	}
	if len(health.PublicIPv4) == 0 {
		clientID, _ := c.Store.GetSecret("tailscale_oauth_client_id")
		clientSecret, _ := c.Store.GetSecret("tailscale_oauth_client_secret")
		device, err := c.tsClient(clientID, clientSecret, settings.Name).GetDevice(ctx, node.TailnetID)
		if err != nil {
			return fmt.Errorf("load balancer endpoint lookup: %w; retaining the previous owned A record", err)
		}
		health.PublicIPv4 = dns.PublicIPv4Endpoints(append(device.Endpoints, device.Addresses...))
	}
	record, preserve, err := dns.LoadBalancerRecord(dns.LoadBalancerEndpoint{Hostname: settings.Hostname, PublicIPv4: health.PublicIPv4})
	if err != nil {
		if preserve {
			return fmt.Errorf("%w; retaining the previous owned A record", err)
		}
		return err
	}
	_, err = c.reconcile(ctx, provider.ID, ownerID, []libdns.Record{record}, false, false)
	return err
}

// ReconcileLoadBalancerDNS removes owned A records after a loadbalancer health
// report expires and reapplies a verified endpoint on the periodic control
// loop. Missing or ambiguous fresh endpoints are retained by design.
func (c *Control) ReconcileLoadBalancerDNS(ctx context.Context) error {
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

func (c *Control) applications(w http.ResponseWriter, _ *http.Request) {
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
	b.WriteString(pageIntro("Infrastructure", "Applications", "Create the Tailscale Service before an ingress sidecar starts. Each application owns its public hostnames, backend port, and allowed ingress nodes."))
	b.WriteString(`<section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Service definition</p><h2>Approve an application</h2></div><span class="pf-pill pf-pill-neutral">Tailscale Service</span></div><form class="pf-form-grid" method="post" action="/api/applications"><label class="pf-field"><span class="pf-label">Application ID</span><input class="pf-input font-mono" name="id" required pattern="[A-Za-z0-9][A-Za-z0-9_-]{0,62}" placeholder="checkout"></label><label class="pf-field"><span class="pf-label">Public hostnames</span><input class="pf-input font-mono" name="public_hostnames" required placeholder="checkout.dev.example.com,checkout.example.com"><small class="pf-help">Only hostnames covered by an enabled exposure policy can publish.</small></label><label class="pf-field"><span class="pf-label">Backend port</span><input class="pf-input" type="number" min="1" max="65535" name="backend_port" required placeholder="8080"></label><label class="pf-field"><span class="pf-label">Service port</span><input class="pf-input" type="number" min="1" max="65535" name="service_port" placeholder="8080"><small class="pf-help">Defaults to the backend port. Different ports use separate relay_assignments and Services.</small></label><label class="pf-field"><span class="pf-label">Allowed ingress node IDs</span><input class="pf-input font-mono" name="allowed_ingress_nodes" placeholder="ingress-node-id,ingress-dev"></label><div class="pf-form-actions"><button class="pf-button" type="submit">Create Service <span aria-hidden="true">→</span></button></div></form></section>`)
	b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Approved services</p><h2>Application inventory</h2></div><span class="pf-pill pf-pill-neutral">` + fmt.Sprintf("%d total", len(apps)) + `</span><a class="pf-text-link" href="/api/services">Service advertisements JSON</a></div>`)
	if len(apps) == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No applications yet</strong><span>Create an application before adding the consolidated Docker exposure label.</span></div>`)
	} else {
		b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Application</th><th>Service</th><th>Public hostnames</th><th>Ports</th><th>Ingress allowlist</th><th>State</th></tr></thead><tbody>`)
		for _, app := range apps {
			state, tone := "Disabled", "neutral"
			if app.Enabled {
				state, tone = "Enabled", "ok"
			}
			allowlist := strings.Join(app.AllowedIngressNodes, ", ")
			if allowlist == "" {
				allowlist = "Any enrolled ingress"
			}
			advertisements := strings.Join(hostState[app.ServiceName], ", ")
			if advertisements == "" {
				advertisements = "no advertisements"
			}
			fmt.Fprintf(&b, `<tr><td data-label="Application"><strong>%s</strong><small class="pf-muted">policy rev %d</small></td><td data-label="Service"><code>%s</code><small class="pf-muted">target · %s</small><small class="pf-muted">hosts · %s</small></td><td data-label="Public hostnames"><code>%s</code></td><td data-label="Ports"><code>%d → %d</code></td><td data-label="Ingress allowlist"><span class="pf-muted">%s</span></td><td data-label="State">%s</td></tr>`, safeText(app.ID), app.PolicyRevision, safeText(app.ServiceName), safeText(app.ServiceFQDN), safeText(advertisements), safeText(strings.Join(app.PublicHostnames, ", ")), app.BackendPort, app.ServicePort, safeText(allowlist), pill(state, tone))
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
	type route struct {
		Hostname string `json:"hostname"`
		Service  string `json:"service"`
		Port     uint16 `json:"port"`
	}
	routes := make([]route, 0)
	for _, app := range apps {
		if !app.Enabled {
			continue
		}
		for _, hostname := range app.PublicHostnames {
			if strings.TrimSpace(hostname) != "" {
				routes = append(routes, route{Hostname: strings.TrimSpace(hostname), Service: app.ServiceFQDN, Port: app.ServicePort})
			}
		}
	}
	var settings struct {
		PublicIPv4 string `json:"loadbalancer_public_ipv4"`
	}
	_ = c.Store.GetJSON("tailnet", &settings)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"routes": routes, "public_ipv4": settings.PublicIPv4})
}

func (c *Control) applicationSave(w http.ResponseWriter, r *http.Request) {
	var app store.ApplicationRecord
	formResponse := !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
	if formResponse {
		app.ID = r.FormValue("id")
		app.ServiceName = r.FormValue("service_name")
		app.ServiceFQDN = r.FormValue("service_fqdn")
		app.PublicHostnames = splitValues(r.FormValue("public_hostnames"))
		app.AllowedIngressNodes = splitValues(r.FormValue("allowed_ingress_nodes"))
		app.BackendPort = uint16(parsePositiveInt(r.FormValue("backend_port")))
		app.ServicePort = uint16(parsePositiveInt(r.FormValue("service_port")))
	} else if json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&app) != nil {
		http.Error(w, "invalid application", 400)
		return
	}
	app.ID = strings.TrimSpace(app.ID)
	if app.ID == "" {
		http.Error(w, "application id is required", 400)
		return
	}
	if !applicationIDPattern.MatchString(app.ID) {
		http.Error(w, "application id must contain only letters, numbers, hyphens, and underscores", 400)
		return
	}
	if app.ServiceName == "" {
		app.ServiceName = "svc:tailscale-private-funnel-app-" + app.ID
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
	service, err := (c.tsClient(clientID, clientSecret, settings.Name)).CreateService(r.Context(), tailscale.Service{Name: app.ServiceName, DisplayName: app.ID, Ports: []string{fmt.Sprintf("tcp:%d", app.ServicePort)}, Tags: []string{"tag:tailscale-private-funnel"}})
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
		http.Error(w, "Service host is not an approved ingress assignment", http.StatusForbidden)
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
// store, the device is an enrolled non-revoked ingress node, and the
// application explicitly allows that node. Unknown advertisements are left
// pending for an administrator to inspect.
func (c *Control) ReconcileServiceApprovals(ctx context.Context) error {
	settings := struct {
		Name string `json:"name"`
	}{}
	if c.Store.GetJSON("tailnet", &settings) != nil || settings.Name == "" {
		return nil
	}
	clientID, err := c.Store.GetSecret("tailscale_oauth_client_id")
	if err != nil {
		return nil
	}
	clientSecret, err := c.Store.GetSecret("tailscale_oauth_client_secret")
	if err != nil {
		return nil
	}
	client := c.tsClient(clientID, clientSecret, settings.Name)
	apps, err := c.Store.Applications()
	if err != nil {
		return err
	}
	for _, app := range apps {
		hosts, listErr := client.ListServiceHosts(ctx, app.ServiceName)
		if listErr != nil {
			return listErr
		}
		for _, host := range hosts {
			allowed := app.Enabled && c.serviceHostAllowed(ctx, client, app, host.NodeID)
			approved := host.ApprovalLevel == "approved:auto" || host.ApprovalLevel == "approved:manual"
			if allowed && !approved {
				_, _ = client.ApproveServiceDevice(ctx, app.ServiceName, host.NodeID, true)
				approved = true
			} else if !allowed && approved {
				_, _ = client.ApproveServiceDevice(ctx, app.ServiceName, host.NodeID, false)
				approved = false
			}
			state := "pending"
			if approved {
				state = "approved"
			}
			if !allowed {
				state = "rejected"
			}
			if err := c.Store.UpsertServiceHost(store.ServiceHostRecord{ServiceName: app.ServiceName, DeviceID: host.NodeID, Approved: approved && allowed, ApprovalState: state}); err != nil {
				return err
			}
		}
	}
	return nil
}

// Service advertisements belong to the enrolled ingress device itself.
func (c *Control) serviceHostAllowed(ctx context.Context, client tailscale.Client, app store.ApplicationRecord, deviceID string) bool {
	node, err := c.Store.NodeByTailnetID(deviceID)
	if err != nil || !applicationAllows(app, node) {
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
	providers, err := c.Store.Providers()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, id := range ids {
		_ = c.Store.RevokeRelays(id)
		for _, provider := range providers {
			if !provider.Enabled {
				continue
			}
			if _, err := c.reconcile(ctx, provider.ID, "ingress:"+id, nil, false, false); err != nil {
				return removed, err
			}
		}
		if err := c.Store.DeleteReport(id); err != nil {
			return removed, err
		}
		removed++
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
	client, clientErr := c.tailscaleRuntimeClient()
	removed := 0
	for _, node := range nodes {
		if clientErr == nil {
			if services, listErr := client.ListServices(ctx); listErr == nil {
				for _, service := range services {
					hosts, hostsErr := client.ListServiceHosts(ctx, service.Name)
					if hostsErr != nil {
						continue
					}
					for _, host := range hosts {
						if host.NodeID == node.TailnetID {
							_, _ = client.ApproveServiceDevice(ctx, service.Name, host.NodeID, false)
						}
					}
				}
			}
			if devices, listErr := client.ListDevices(ctx); listErr == nil {
				for _, device := range devices {
					if device.NodeID == node.TailnetID {
						_ = client.DeleteDevice(ctx, device.ID)
					}
				}
			}
		}
		_ = c.Store.RevokeNode(node.ID)
		_ = c.Store.RevokeRelays(node.ID)
		_ = c.Store.DeleteReport(node.ID)
		_ = c.Store.Audit("node.sweeper.cleanup:"+node.ID, "stale heartbeat")
		removed++
	}
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
	p := principal(r)
	node, nodeErr := c.Store.Node(strings.TrimSpace(envelope.NodeID))
	if p.NodeID == "" || nodeErr != nil || node.Mode != "ingress" || node.TailnetID == "" || node.TailnetID != p.NodeID {
		http.Error(w, fmt.Sprintf("report node does not match enrolled tailnet identity (report=%s whois=%s stored=%s err=%v)", envelope.NodeID, p.NodeID, node.TailnetID, nodeErr), 403)
		return
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
		Exposures  []dns.IngressExposure `json:"exposures"`
	}
	if err := json.Unmarshal(envelope.Payload, &reportPayload); err != nil {
		http.Error(w, "invalid report payload", http.StatusBadRequest)
		return
	}
	providerID := reportPayload.ProviderID
	if providerID == "" {
		if providers, listErr := c.Store.Providers(); listErr == nil {
			for _, provider := range providers {
				if provider.Enabled {
					providerID = provider.ID
					break
				}
			}
		}
	}
	var desired []libdns.Record
	ingressIDs := []string{envelope.NodeID, p.NodeID, node.TailnetID, node.Hostname}
	roles := p.RoleSelectors()
	applications, applicationsErr := c.Store.Applications()
	if applicationsErr != nil {
		http.Error(w, applicationsErr.Error(), http.StatusInternalServerError)
		return
	}
	for i := range reportPayload.Exposures {
		exposure := &reportPayload.Exposures[i]
		application, applicationOK := approvedApplication(applications, *exposure)
		if !applicationOK || !applicationAllows(application, node) {
			http.Error(w, "exposure does not match an enabled application Service", http.StatusForbidden)
			return
		}
		if !exposure.Reachable {
			continue
		}
		exposure.ApplicationID = application.ID
		exposure.ServiceName = application.ServiceName
		records, recordErr := dns.IngressRecords(*exposure)
		if recordErr != nil {
			http.Error(w, recordErr.Error(), http.StatusBadRequest)
			return
		}
		if !c.exposureAllowed(*exposure, ingressIDs, roles) {
			http.Error(w, "ingress exposure is not allowed by the control policy", http.StatusForbidden)
			return
		}
		desired = append(desired, records...)
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
	activeRelays := map[string]bool{}
	for _, exposure := range reportPayload.Exposures {
		activeRelays[relayRecordID(envelope.NodeID, exposure)] = true
		if err := c.Store.UpsertRelay(store.RelayAssignment{
			ID: relayRecordID(envelope.NodeID, exposure), ApplicationID: exposure.ApplicationID, NodeID: envelope.NodeID,
			ContainerID: exposure.ContainerID, BackendIP: exposure.BackendIP, BackendPort: exposure.Port,
			Networks: exposure.Networks, Reachable: exposure.Reachable, ConfigRevision: envelope.Timestamp,
			LastSeen: time.Now().Unix(),
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := c.Store.ReconcileRelays(envelope.NodeID, activeRelays); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if providerID != "" {
		if _, syncErr := c.reconcile(r.Context(), providerID, "ingress:"+envelope.NodeID, desired, false, false); syncErr != nil {
			http.Error(w, "report accepted but DNS reconciliation failed: "+syncErr.Error(), http.StatusBadGateway)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

func relayRecordID(nodeID string, exposure dns.IngressExposure) string {
	return strings.Join([]string{nodeID, exposure.ServiceName, exposure.PublicHostname, fmt.Sprintf("%d", exposure.Port)}, ":")
}

func approvedApplication(applications []store.ApplicationRecord, exposure dns.IngressExposure) (store.ApplicationRecord, bool) {
	hostname := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(exposure.PublicHostname), "."))
	for _, application := range applications {
		if !application.Enabled || application.ServiceName != strings.TrimSpace(exposure.ServiceName) || application.BackendPort != exposure.Port {
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
	if r.URL.Path == "/ingresses" {
		mode = "ingress"
	}
	nodes, _ := c.Store.Nodes(mode)
	title, kicker, description, href := "Load balancers", "Infrastructure", "The public edge terminates TLS and resolves approved Tailscale Services.", "/enroll/loadbalancer"
	if mode == "ingress" {
		title, description, href = "Ingress nodes", "Ingress nodes discover only labeled HTTP containers on the user-defined Docker networks and report signed desired state to the control plane.", "/enroll/ingress"
	}
	var b strings.Builder
	b.WriteString(pageIntro(kicker, title, description))
	b.WriteString(`<div class="pf-action-row"><a class="pf-button" href="` + href + `">Create ` + strings.ToLower(strings.TrimSuffix(title, "s")) + ` enrollment <span aria-hidden="true">→</span></a><a class="pf-secondary-button" href="/api/nodes/` + mode + `">View JSON</a></div><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Node inventory</p><h2>Connected ` + strings.ToLower(title) + `</h2></div><span class="pf-pill pf-pill-neutral">` + fmt.Sprintf("%d total", len(nodes)) + `</span></div>`)
	if len(nodes) == 0 {
		b.WriteString(`<div class="pf-empty"><strong>No ` + strings.ToLower(title) + ` enrolled</strong><span>Use the generated deployment script to provision a node, then return here to confirm its identity and report state.</span></div>`)
	} else {
		b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Hostname</th><th>Tailnet identity</th><th>Health</th><th>Last report</th><th>Action</th></tr></thead><tbody>`)
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
			action := `<div class="pf-inline-actions"><a class="pf-text-link" href="/api/nodes/` + safeText(mode) + `">Inspect</a>`
			if !revoked && nodeID != "" {
				action += `<form method="post" action="/api/nodes/` + safeText(nodeID) + `/revoke"><button class="pf-link-button pf-link-danger" type="submit">Revoke</button></form>`
			}
			action += `</div>`
			fmt.Fprintf(&b, `<tr><td data-label="Hostname"><strong>%s</strong><small class="pf-muted">%s</small></td><td data-label="Tailnet identity"><code>%s</code></td><td data-label="Health">%s</td><td data-label="Last report"><span class="pf-muted">%s</span></td><td data-label="Action">%s</td></tr>`, safeText(stringValue(node, "hostname", "—")), safeText(stringValue(node, "mode", mode)), safeText(nodeID), pill(state, tone), safeText(unixLabel(last)), action)
		}
		b.WriteString(`</tbody></table></div>`)
	}
	b.WriteString(`</section>`)
	if mode == "ingress" {
		b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Ingress guidance</p><h2>Keep the route private</h2></div></div><p class="pf-muted">The supervisor automatically selects user-defined networks on each labeled target; set local deny patterns for sensitive networks. Use <code>tailscale-private-funnel.expose=PORT:hostname:http</code> labels. The supervisor creates one isolated forwarding sidecar per backend port. A hostname must match an enabled policy rule before DNS changes.</p></section>`)
	}
	render(w, title, b.String())
}
func (c *Control) enroll(w http.ResponseWriter, r *http.Request) {
	mode := chi.URLParam(r, "mode")
	if mode != "loadbalancer" && mode != "ingress" {
		http.Error(w, "invalid mode", 400)
		return
	}
	description := "The generated deployment script downloads the verified binary, joins Tailscale, installs a system service, persists identity, and starts the loadbalancer."
	if mode == "ingress" {
		description = "The generated deployment script joins Tailscale, installs a system service, validates Docker socket access and local network policy, and starts label discovery. A ingress report is accepted only when the exposure policy allows every hostname."
	}
	render(w, "Enrollment", pageIntro("Infrastructure", strings.Title(mode)+" enrollment", description)+fmt.Sprintf(`<div class="pf-alert pf-alert-info"><strong>One-use enrollment.</strong> The bootstrap key expires in 15 minutes. The joined identity survives reboot; only reset or revocation removes it. OAuth credentials stay in the control service.</div><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Step 1 · Identity</p><h2>Choose a node hostname</h2></div><span class="pf-pill pf-pill-neutral">1 of 2</span></div><form id="enrollment-form" class="pf-form-grid"><label class="pf-field"><span class="pf-label">Node hostname</span><input class="pf-input" name="hostname" value="%s-node" required><small class="pf-help">Use a stable name that is easy to identify in Tailscale.</small></label><div class="pf-form-actions"><button class="pf-button" type="submit">Generate deployment script <span aria-hidden="true">→</span></button></div></form></section><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Step 2 · Deploy</p><h2>One-copy native deployment</h2></div><span class="pf-pill pf-pill-neutral">Linux + systemd</span></div><p class="pf-muted">The script downloads a verified release binary, enrolls the node, installs a system-level systemd service, and starts it. It checks loadbalancer ports or ingress Docker access before changing anything.</p><pre class="pf-command" id="enrollment-result" aria-live="polite">Complete the hostname step to generate a deployment script.</pre></section><p><a class="pf-text-link" href="/%ss">Back to %ss <span aria-hidden="true">→</span></a></p><script>document.getElementById('enrollment-form').onsubmit=async function(e){e.preventDefault();const result=document.getElementById('enrollment-result');const r=await fetch('/api/enrollments/%s',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({hostname:this.hostname.value})});const text=await r.text();if(!r.ok){result.textContent=text;return}let data;try{data=JSON.parse(text)}catch(_){result.textContent=text;return}const lines=['Enrollment created. Keep the code private until the target node has joined.','','Steps:',...(data.instructions||[]).map((step,i)=>(i+1)+'. '+step),'','One-copy deployment script:',data.deployment_script||'','', 'Ingress label example:',data.label_example||'','', 'Exposure policy:',data.policy_url||'/policies'];result.textContent=lines.join('\n')}</script>`, mode, mode, strings.Title(mode), mode))
}
func requiredNodeTag(mode string) string {
	return "tag:tailscale-private-funnel"
}

func (c *Control) enrollmentCreate(w http.ResponseWriter, r *http.Request) {
	mode := chi.URLParam(r, "mode")
	if mode != "loadbalancer" && mode != "ingress" {
		http.Error(w, "invalid mode", 400)
		return
	}
	var input struct {
		Hostname string `json:"hostname"`
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		_ = json.NewDecoder(r.Body).Decode(&input)
	} else {
		input.Hostname = r.FormValue("hostname")
	}
	if strings.TrimSpace(input.Hostname) == "" {
		input.Hostname = mode + "-node"
	}
	e, code, err := c.Store.CreateEnrollment(mode, strings.TrimSpace(input.Hostname))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("enrollment.create:"+e.ID, identityFromRequest(r))
	var settings struct {
		Tailnet          string `json:"name"`
		Image            string `json:"image"`
		ManagementURL    string `json:"management_url"`
		CertEmail        string `json:"cert_email"`
		LoadBalancerHost string `json:"loadbalancer_hostname"`
		PublicIPv4       string `json:"loadbalancer_public_ipv4"`
		LabelKeys        string `json:"label_keys"`
		DenyNetworks     string `json:"deny_networks"`
	}
	_ = c.Store.GetJSON("tailnet", &settings)
	if settings.Image == "" {
		settings.Image = "ghcr.io/tailscale-x/tailscale-private-funnel:dev"
	}
	releaseVersion := releaseTagFromImage(settings.Image)
	installCommand := fmt.Sprintf("curl -fsSL %s | sh -s -- --version %s", shellQuote("https://raw.githubusercontent.com/tailscale-x/tailscale-private-funnel/"+releaseVersion+"/scripts/install.sh"), shellQuote(releaseVersion))
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
	deployArgs += " --control-key " + shellQuote(base64.RawStdEncoding.EncodeToString(controlPub))
	if settings.CertEmail != "" {
		deployArgs += " --cert-email " + shellQuote(settings.CertEmail)
	}
	if settings.LoadBalancerHost != "" {
		deployArgs += " --loadbalancer-hostname " + shellQuote(settings.LoadBalancerHost)
	}
	if settings.PublicIPv4 != "" && mode == "loadbalancer" {
		deployArgs += " --public-ipv4 " + shellQuote(settings.PublicIPv4)
	}
	if settings.Image != "" {
		deployArgs += " --image " + shellQuote(settings.Image)
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
	deploymentScript := strings.Join([]string{
		"#!/bin/sh", "set -eu", "", installCommand,
		"binary=\"${XDG_BIN_HOME:-$HOME/.local/bin}/tailscale-private-funnel\"",
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
		instructions = append(instructions, "Publish TCP ports 80 and 443 only on this loadbalancer. Control reconciles its owned A record from the configured public IPv4 or the enrolled Tailscale device endpoint.")
	} else {
		instructions = append(instructions,
			"The ingress supervisor needs host Docker socket permissions; relay_assignments receive no socket and are attached automatically to each selected user-defined target network.",
			"Every label hostname must match an enabled rule at /policies or the control service will reject the signed report without changing DNS.",
		)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"enrollment": e, "code": code, "deployment_script": deploymentScript, "image": settings.Image, "instructions": instructions, "policy_url": "/policies", "label_example": `tailscale-private-funnel.expose=8080:app.dev.example.com:http`})
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
	if err := c.Store.BindEnrollment(id, code, input.PublicKey, p.NodeID, p.Addresses); err != nil {
		http.Error(w, "enrollment already consumed or invalid", http.StatusConflict)
		return
	}

	_ = c.Store.Audit("enrollment.bind-key:"+id, identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) nodeList(w http.ResponseWriter, r *http.Request) {
	mode := chi.URLParam(r, "mode")
	if mode != "loadbalancer" && mode != "ingress" {
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
	_ = c.Store.RevokeRelays(id)
	_ = c.Store.Audit("node.revoke:"+id, identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
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

func (c *Control) operationsSync(w http.ResponseWriter, r *http.Request) {
	var in syncInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "invalid sync request", http.StatusBadRequest)
		return
	}
	in.DryRun = true
	body, _ := json.Marshal(in)
	req := httptest.NewRequestWithContext(r.Context(), http.MethodPost, "/api/sync", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = WithPrincipal(req, principal(r))
	rr := httptest.NewRecorder()
	c.sync(rr, req)
	for key, values := range rr.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(rr.Code)
	_, _ = w.Write(rr.Body.Bytes())
}
