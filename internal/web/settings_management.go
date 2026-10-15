package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const defaultRelease = "v1.1.0"

var releasePattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type nodeDeployment struct {
	DeploymentTarget string `json:"deployment_target,omitempty"`
	AddressSource    string `json:"address_source,omitempty"`
	HelmRelease      string `json:"helm_release,omitempty"`
	PublicService    string `json:"public_service,omitempty"`
	InstallationName string `json:"installation_name,omitempty"`
	WatchNamespaces  string `json:"watch_namespaces,omitempty"`
	NodeSelector     string `json:"node_selector,omitempty"`

	KubernetesNamespace  string `json:"kubernetes_namespace,omitempty"`
	GatewayClass         string `json:"gateway_class,omitempty"`
	IngressClass         string `json:"ingress_class,omitempty"`
	StateDir             string `json:"state_dir,omitempty"`
	DataDir              string `json:"data_dir,omitempty"`
	LoadBalancerHostname string `json:"loadbalancer_hostname"`
	CertEmail            string `json:"cert_email"`
	LabelKeys            string `json:"label_keys"`
	DenyNetworks         string `json:"deny_networks"`
}

func (c *Control) nodeDeployment(id string) (nodeDeployment, error) {
	var d nodeDeployment
	err := c.Store.GetJSON("node_deployment:"+id, &d)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	return d, err
}
func (c *Control) settings(w http.ResponseWriter, r *http.Request) {
	var current map[string]any
	if err := c.Store.GetJSON("tailnet", &current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Unable to read settings", 503)
		return
	}
	value := func(key string) string { return safeText(stringValue(current, key, "")) }
	release := stringValue(current, "release_version", "")
	if release == "" {
		release = releaseTagFromImage(stringValue(current, "image", ""))
		if !releasePattern.MatchString(release) {
			release = defaultRelease
		}
	}
	b := pageIntro("Access", "Control settings", "Control identity, OAuth credentials and defaults for new enrollments.")
	b += `<section class="pf-surface"><h2>Current control identity</h2><dl><dt>Tailnet</dt><dd><code>` + value("name") + `</code></dd><dt>Role tag</dt><dd><code>tag:tailscale-private-funnel</code></dd></dl><p class="pf-help">Identity is established by control join. Changing tailnets requires an explicit reset and rejoin; saving settings does not reconnect the running node.</p><a class="pf-text-link" href="/access">Configure access roles</a></section>`
	if requestAccess(r).Admin {
		b += `<p><a class="pf-text-link" href="/kubernetes/control">Deploy a control installation on Kubernetes</a></p>`
		b += `<section class="pf-surface pf-form-surface"><h2>Tailscale OAuth credentials</h2><p class="pf-help">Credentials are encrypted and never returned to the browser. Blank replacements preserve stored values. Testing and saving verify authentication plus Devices Read and Users Read access in this tailnet; enrollment and Service operations still require their corresponding scopes.</p><form class="pf-form-grid" method="post" hx-post="/settings" hx-target="#oauth-result"><input type="hidden" name="section" value="oauth"><label class="pf-field"><span class="pf-label">OAuth client ID</span><input class="pf-input" name="client_id" autocomplete="off" placeholder="Leave blank to preserve"></label><label class="pf-field"><span class="pf-label">OAuth client secret</span><input class="pf-input" name="client_secret" type="password" autocomplete="new-password" placeholder="Leave blank to preserve"></label><div class="pf-form-actions"><button name="action" value="save" class="pf-button">Test and save credentials</button><button name="action" value="test" class="pf-secondary-button">Test only</button></div><p id="oauth-result" role="status"></p></form></section>`
		b += `<section class="pf-surface pf-form-surface"><h2>New enrollment defaults</h2><p class="pf-help">Applies to new nodes only. Existing management URLs, binaries and systemd services are not changed. Load-balancer and gateway deployment choices are made on their enrollment pages.</p><form class="pf-form-grid" method="post" hx-post="/settings" hx-target="#deployment-result"><input type="hidden" name="section" value="deployment"><label class="pf-field"><span class="pf-label">Management URL for new nodes</span><input class="pf-input" name="management_url" type="url" value="` + value("management_url") + `"><small class="pf-help">Clearing this prevents new enrollments until a URL is configured.</small></label><label class="pf-field"><span class="pf-label">Published binary release</span><input class="pf-input font-mono" name="release_version" required value="` + safeText(release) + `" placeholder="v1.1.0"><small class="pf-help">Must be a published version with Linux binaries and checksums. The matching release image is available for container deployments.</small></label><div class="pf-form-actions"><button class="pf-button">Save enrollment defaults</button></div><p id="deployment-result" role="status"></p></form></section>`
	} else {
		b += `<div class="pf-alert pf-alert-info">Read only. An administrator can rotate OAuth credentials or edit enrollment defaults.</div>`
	}
	b += `<script>document.body.addEventListener('htmx:beforeSwap',e=>{if(e.detail.target?.id.endsWith('-result')){e.detail.shouldSwap=true;e.detail.isError=false;}});</script>`
	render(w, "Control settings", b)
}
func validManagementURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Use an HTTP or HTTPS management URL without credentials, query or fragment")
	}
	return nil
}
func (c *Control) checkRelease(ctx context.Context, version string) error {
	if !releasePattern.MatchString(version) {
		return fmt.Errorf("Use a published version such as v1.1.0")
	}
	if c.ReleaseCheck != nil {
		return c.ReleaseCheck(ctx, version)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, asset := range []string{"tailscale-private-funnel-linux-amd64", "tailscale-private-funnel-linux-arm64", "SHA256SUMS"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, "https://github.com/tailscale-x/tailscale-private-funnel/releases/download/"+version+"/"+asset, nil)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("Unable to verify release; retry before saving")
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("Release %s is missing %s", version, asset)
		}
	}
	return nil
}
func (c *Control) saveSettings(w http.ResponseWriter, r *http.Request) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid settings", 400)
		return
	}
	var current map[string]any
	err := c.Store.GetJSON("tailnet", &current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Unable to read settings", 503)
		return
	}
	if current == nil {
		current = map[string]any{}
	}
	section := r.FormValue("section")
	allowed := map[string]bool{"section": true}
	if section == "oauth" {
		allowed["client_id"] = true
		allowed["client_secret"] = true
		allowed["action"] = true
	} else if section == "deployment" {
		allowed["management_url"] = true
		allowed["release_version"] = true
	}
	for key := range r.PostForm {
		if !allowed[key] {
			http.Error(w, "Unsupported settings field: "+key, 400)
			return
		}
	}
	switch section {
	case "oauth":
		id := strings.TrimSpace(r.FormValue("client_id"))
		secret := strings.TrimSpace(r.FormValue("client_secret"))
		if id == "" {
			id, _ = c.Store.GetSecret("tailscale_oauth_client_id")
		}
		if secret == "" {
			secret, _ = c.Store.GetSecret("tailscale_oauth_client_secret")
		}
		if id == "" || secret == "" {
			http.Error(w, "Client ID and secret are required", 400)
			return
		}
		client := c.tsClient(id, secret, stringValue(current, "name", ""))
		client.ClientID = id
		client.ClientSecret = secret
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if _, err = client.ListDevices(ctx); err == nil {
			_, err = client.ListUsers(ctx)
		}
		if err != nil {
			http.Error(w, "Credential test failed. Check the client, tailnet and Devices Read / users:read permissions. Stored credentials were not changed.", 502)
			return
		}
		if r.FormValue("action") == "test" {
			fmt.Fprint(w, "Credentials verified. No changes saved.")
			return
		}
		if err = c.Store.SetSecrets(map[string]string{"tailscale_oauth_client_id": id, "tailscale_oauth_client_secret": secret}); err != nil {
			http.Error(w, "Unable to save credentials", 500)
			return
		}
		c.roleMu.Lock()
		c.rolesExpire = time.Time{}
		c.roleUsers = nil
		c.roleMu.Unlock()
		_ = c.Store.Audit("settings.oauth.rotate", identityFromRequest(r))
		settingsReply(w, r, "Credentials verified and saved.")
	case "deployment":
		management := strings.TrimSpace(r.FormValue("management_url"))
		version := strings.TrimSpace(r.FormValue("release_version"))
		if err = validManagementURL(management); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err = c.checkRelease(r.Context(), version); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		current["management_url"] = management
		current["release_version"] = version
		current["image"] = "ghcr.io/tailscale-x/tailscale-private-funnel:" + version
		if err = c.Store.SetJSON("tailnet", current); err != nil {
			http.Error(w, "Unable to save enrollment defaults", 500)
			return
		}
		_ = c.Store.Audit("settings.enrollment.defaults", identityFromRequest(r))
		settingsReply(w, r, "Defaults saved. Applies to new enrollments only.")
	default:
		http.Error(w, "Select OAuth or enrollment defaults; identity and node fields are not editable here.", 400)
	}
}
func settingsReply(w http.ResponseWriter, r *http.Request, message string) {
	if r.Header.Get("HX-Request") == "true" {
		fmt.Fprint(w, message)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (c *Control) nodeSettings(w http.ResponseWriter, r *http.Request) {
	node, err := c.Store.Node(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, err := c.nodeDeployment(node.ID)
	if err != nil {
		http.Error(w, "Unable to read node settings", 503)
		return
	}
	parent, label := "/loadbalancers", "load balancers"
	if store.IsGateway(node.Mode) {
		parent, label = "/gateways", "gateway nodes"
	}
	b := pageIntro("Infrastructure", c.nodeDisplayName(node.ID), "Node identity and reported network state.") + `<div class="pf-action-row"><a class="pf-text-link" data-node-navigation="` + parent + `" href="` + parent + `">Back to ` + label + `</a></div>` + `<section class="pf-surface"><h2>Identity</h2><p>Internal ID: <code>` + safeText(node.ID) + `</code></p><p>Tailscale hostname: <code>` + safeText(node.Hostname) + `</code></p></section>`
	if requestAccess(r).Admin {
		b += `<section class="pf-surface"><h2>Display name</h2>` + renameForm("nodes", node.ID, node.Name) + `<p class="pf-help">Renaming does not change the Tailscale hostname, identity or routing.</p></section>` + renameScript
	}
	if node.Mode == "loadbalancer" {
		var endpoints struct {
			PublicIPv4 []string `json:"public_ipv4"`
			CheckedAt  int64    `json:"checked_at"`
		}
		_ = c.Store.GetJSON("node_endpoints:"+node.ID, &endpoints)
		if d.AddressSource != "kubernetes" {
			b += `<section class="pf-surface"><h2>Load-balancer DNS</h2><p>Public hostname: <code>` + safeText(d.LoadBalancerHostname) + `</code></p><p>Tailscale public IPv4 endpoints: <code>` + safeText(strings.Join(endpoints.PublicIPv4, ", ")) + `</code></p><p class="pf-help">Control fetches this enrolled device’s endpoints from Tailscale. Exactly one distinct public IPv4 is required. Missing or ambiguous endpoints preserve the previous owned A record. No manual override or node-reported address is used.</p></section>`
		}
		if d.AddressSource == "kubernetes" {
			var observation publicEndpoint
			_ = c.Store.GetJSON("node_public_endpoint:"+node.ID, &observation)
			allocation := "Waiting for Service observation"
			if observation.ObservedAt > 0 {
				allocation = "Allocation pending"
				if len(observation.Ingress) > 0 {
					allocation = "Allocation observed; public address not ready"
				}
				if len(endpoints.PublicIPv4) > 0 {
					allocation = "Public IPv4 ready"
				}
				if observation.ObservedAt < time.Now().Add(-time.Minute).Unix() {
					allocation = "Service observation stale; retaining owned DNS"
				}
			}
			var health map[string]any
			_ = c.Store.GetJSON("node_health:"+node.ID, &health)
			b += `<section class="pf-surface"><h2>Public address readiness</h2><p role="status">` + safeText(allocation) + `</p><p class="pf-help">` + safeText(diagnosticValue(health["dns_sync_error"])) + `</p></section>`
			b += `<section class="pf-surface"><h2>Kubernetes public Service</h2><p>Namespace: <code>` + safeText(d.KubernetesNamespace) + `</code> · Service: <code>` + safeText(d.PublicService) + `</code></p><p>Public addresses: <code>` + safeText(strings.Join(endpoints.PublicIPv4, ", ")) + `</code></p><p class="pf-help">Addresses come from authenticated, signed Kubernetes Service observations. Pending allocation or invalid addresses retain previous owned records. Pod, node and outbound Tailscale addresses are never substituted. Identity and certificates persist on this release’s PVC.</p></section>`
		}

	}
	if node.Mode == "tailnet-gateway" {
		var peers struct {
			Tags       []string `json:"tags"`
			ReceivedAt int64    `json:"received_at"`
		}
		_ = c.Store.GetJSON("node_peers:"+node.ID, &peers)
		b += `<section class="pf-surface"><h2>Tailnet backend access</h2><p>Effective tags: <code>` + safeText(strings.Join(peers.Tags, ", ")) + `</code></p><p class="pf-help">Assign additional tags and grants in Tailscale. Local Funnel does not modify ACLs. Backend visibility is not a successful connection test.</p>`
		if requestAccess(r).Admin && !node.Revoked {
			b += `<a class="pf-button" href="/tailnet-exposures?gateway=` + safeText(node.ID) + `">Expose a tailnet machine</a>`
		}
		b += `</section>`
	}

	if store.IsGateway(node.Mode) {
		var health map[string]any
		_ = c.Store.GetJSON("node_health:"+node.ID, &health)
		b += `<section class="pf-surface"><h2>` + gatewayTitle(node.Mode) + ` · diagnostics</h2><p class="pf-help">` + gatewayHelp(node.Mode) + `</p><p>Tailnet identity: <code>` + safeText(node.TailnetID) + `</code></p>`
		var ack struct {
			Revision string   `json:"revision"`
			Services []string `json:"services"`
			At       int64    `json:"at"`
		}
		_ = c.Store.GetJSON("node_config_ack:"+node.ID, &ack)
		b += `<p>Effective tags: <code>` + safeText(fmt.Sprint(health["effective_tags"])) + `</code></p>`
		if node.Mode == "k8s-gateway" {
			children, e := c.Store.Forwarders(node.ID)
			b += `<h3>Node-local forwarders</h3><p class="pf-help">Only ready backends on each forwarder’s own node are advertised. No cross-node fallback.</p><div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Node</th><th>Tailscale identity</th><th>Health / freshness</th><th>Local backends</th><th>Services</th></tr></thead><tbody>`
			if e != nil {
				b += `<tr><td colspan="5">Forwarder inventory unavailable</td></tr>`
			}
			for _, child := range children {
				var health map[string]any
				_ = c.Store.GetJSON("node_health:"+child.ID, &health)
				var childAck struct {
					Services []string `json:"services"`
				}
				_ = c.Store.GetJSON("node_config_ack:"+child.ID, &childAck)
				state := "Waiting"
				if child.Revoked {
					state = "Revoked"
				} else if child.LastReport > time.Now().Add(-time.Minute).Unix() {
					state = "Online"
				} else if child.LastReport > 0 {
					state = "Stale"
				}
				b += fmt.Sprintf(`<tr><td data-label="Node">%s</td><td data-label="Identity"><code>%s</code></td><td data-label="Health">%s</td><td data-label="Local backends">%s</td><td data-label="Services">%d</td></tr>`, safeText(child.Hostname), safeText(child.TailnetID), safeText(state+diagnosticSuffix(health["withdrawal_reason"])), safeText(diagnosticValue(health["local_endpoints"])), uniqueServiceCount(childAck.Services))
			}
			if len(children) == 0 {
				b += `<tr><td colspan="5">Waiting for DaemonSet forwarders to enroll.</td></tr>`
			}
			b += `</tbody></table></div>`
			var snapshot nodeconfig.KubernetesSnapshot
			_ = c.Store.GetJSON("node_kubernetes:"+node.ID, &snapshot)
			for _, host := range snapshot.Hosts {
				b += `<p><code>` + safeText(host.Hostname) + `</code> · ` + safeText(strings.Join(host.ResourceIDs, ", ")) + `</p>`
			}
			for resource, problem := range snapshot.Errors {
				b += `<p class="pf-alert pf-alert-error">` + safeText(resource) + `: ` + safeText(problem) + `</p>`
			}
		}
		b += `<p>Configuration revision: <code>` + safeText(ack.Revision) + `</code></p>`
		if node.Mode == "k8s-gateway" {
			b += `<p class="pf-help">The controller validates routing configuration. Service listeners and backend reachability are reported by the node-local forwarders above.</p>`
		} else {
			b += `<p>Service listeners: ` + fmt.Sprint(uniqueServiceCount(ack.Services)) + ` · Reachable backends: ` + safeText(diagnosticValue(health["reachable"])) + `</p>`
		}
		if requestAccess(r).Admin && !node.Revoked {
			if node.Mode == "tailnet-gateway" {
				b += `<p><a class="pf-button" href="/tailnet-exposures?gateway=` + safeText(node.ID) + `">Expose a tailnet machine</a></p>`
			}
			b += `<button class="pf-secondary-button" onclick="if(confirm('Revoke this gateway? Its routes, owned DNS and Tailscale identity will be withdrawn.'))fetch('/api/nodes/` + safeText(node.ID) + `/revoke',{method:'POST'}).then(r=>r.json()).then(d=>{alert(d.pending?'Revoked; cleanup is pending. Check Operations.':'Gateway revoked and cleaned up.');location.reload()})">Revoke gateway</button>`
		}
		b += `</section>`
	}

	render(w, "Node settings", b)
}
func validateNodeDeployment(d nodeDeployment, mode string) error {
	if d.DeploymentTarget == "kubernetes" && mode != "loadbalancer" && mode != "k8s-gateway" {
		return fmt.Errorf("Kubernetes deployment applies to load balancers and Kubernetes gateways")
	}
	if d.DeploymentTarget != "" && d.DeploymentTarget != "native" && d.DeploymentTarget != "kubernetes" {
		return fmt.Errorf("invalid deployment target")
	}
	if d.AddressSource != "" {
		return fmt.Errorf("address_source is derived from deployment target")
	}
	if d.PublicService != "" {
		return fmt.Errorf("public_service is generated by control")
	}
	kube := mode == "k8s-gateway" || mode == "loadbalancer" && d.DeploymentTarget == "kubernetes"
	for _, value := range []string{d.KubernetesNamespace, d.GatewayClass, d.IngressClass, d.InstallationName, d.HelmRelease} {
		if value != "" && (!kube || len(value) > 63 || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`).MatchString(value)) {
			return fmt.Errorf("Kubernetes namespace and class names must be DNS labels and only apply to Kubernetes gateways")
		}
	}

	if len(d.InstallationName) > 31 {
		return fmt.Errorf("installation name must be at most 31 characters")
	}
	if d.NodeSelector != "" {
		var selector map[string]string
		if mode != "k8s-gateway" || json.Unmarshal([]byte(d.NodeSelector), &selector) != nil || selector == nil {
			return fmt.Errorf("node selector must be a JSON object of label keys and values")
		}
		for key, value := range selector {
			if len(kvalidation.IsQualifiedName(key)) > 0 || len(kvalidation.IsValidLabelValue(value)) > 0 {
				return fmt.Errorf("invalid Kubernetes node selector label")
			}
		}
	}
	for _, ns := range splitValues(d.WatchNamespaces) {
		if mode != "k8s-gateway" || len(ns) > 63 || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`).MatchString(ns) {
			return fmt.Errorf("invalid watched namespace")
		}
	}
	if mode == "k8s-gateway" && (d.StateDir != "" || d.DataDir != "") {
		return fmt.Errorf("Kubernetes identity is persisted in installation-scoped Secrets; local paths are chart managed")
	}
	for _, path := range []string{d.StateDir, d.DataDir} {
		if path != "" && (!filepath.IsAbs(path) || filepath.Clean(path) == "/" || strings.ContainsAny(path, "\n\r")) {
			return fmt.Errorf("state/data paths must be absolute directories")
		}
	}
	if mode == "loadbalancer" && policy.ValidateHostname(d.LoadBalancerHostname) != nil {
		return fmt.Errorf("A valid load-balancer DNS hostname is required")
	}
	if d.CertEmail != "" {
		if address, err := mail.ParseAddress(d.CertEmail); err != nil || address.Address != d.CertEmail {
			return fmt.Errorf("Invalid certificate email")
		}
	}

	if mode != "docker-gateway" && (d.LabelKeys != "" || d.DenyNetworks != "") {
		return fmt.Errorf("Docker settings apply only to gateway")
	}
	if store.IsGateway(mode) && d.CertEmail != "" {
		return fmt.Errorf("Certificate email applies only to load balancers")
	}
	for _, pattern := range splitValues(d.DenyNetworks) {
		if _, err := filepath.Match(pattern, "test"); err != nil {
			return fmt.Errorf("Invalid Docker deny pattern: %s", pattern)
		}
	}
	for _, key := range splitValues(d.LabelKeys) {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`).MatchString(key) {
			return fmt.Errorf("Invalid exposure label alias")
		}
	}
	return nil
}

func diagnosticValue(value any) string {
	if value == nil {
		return "Not reported"
	}
	return fmt.Sprint(value)
}
func diagnosticSuffix(value any) string {
	if value == nil || fmt.Sprint(value) == "" {
		return ""
	}
	return " · " + fmt.Sprint(value)
}
func uniqueServiceCount(services []string) int {
	unique := map[string]bool{}
	for _, s := range services {
		unique[s] = true
	}
	return len(unique)
}
