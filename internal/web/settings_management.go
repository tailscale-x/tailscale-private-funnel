package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
)

const defaultRelease = "v1.1.0"

var releasePattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type nodeDeployment struct {
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
		b += `<section class="pf-surface pf-form-surface"><h2>Tailscale OAuth credentials</h2><p class="pf-help">Credentials are encrypted and never returned to the browser. Blank replacements preserve stored values. Testing and saving verify authentication plus Devices Read and Users Read access in this tailnet; enrollment and Service operations still require their corresponding scopes.</p><form class="pf-form-grid" method="post" hx-post="/settings" hx-target="#oauth-result"><input type="hidden" name="section" value="oauth"><label class="pf-field"><span class="pf-label">OAuth client ID</span><input class="pf-input" name="client_id" autocomplete="off" placeholder="Leave blank to preserve"></label><label class="pf-field"><span class="pf-label">OAuth client secret</span><input class="pf-input" name="client_secret" type="password" autocomplete="new-password" placeholder="Leave blank to preserve"></label><div class="pf-form-actions"><button name="action" value="save" class="pf-button">Test and save credentials</button><button name="action" value="test" class="pf-secondary-button">Test only</button></div><p id="oauth-result" role="status"></p></form></section>`
		b += `<section class="pf-surface pf-form-surface"><h2>New enrollment defaults</h2><p class="pf-help">Applies to new nodes only. Existing management URLs, binaries and systemd services are not changed. Load-balancer and ingress deployment choices are made on their enrollment pages.</p><form class="pf-form-grid" method="post" hx-post="/settings" hx-target="#deployment-result"><input type="hidden" name="section" value="deployment"><label class="pf-field"><span class="pf-label">Management URL for new nodes</span><input class="pf-input" name="management_url" type="url" value="` + value("management_url") + `"><small class="pf-help">Clearing this prevents new enrollments until a URL is configured.</small></label><label class="pf-field"><span class="pf-label">Published binary release</span><input class="pf-input font-mono" name="release_version" required value="` + safeText(release) + `" placeholder="v1.1.0"><small class="pf-help">Must be a published version with Linux binaries and checksums. The matching release image is available for container deployments.</small></label><div class="pf-form-actions"><button class="pf-button">Save enrollment defaults</button></div><p id="deployment-result" role="status"></p></form></section>`
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
	if node.Mode == "ingress" {
		parent, label = "/ingresses", "ingress nodes"
	}
	b := pageIntro("Infrastructure", c.nodeDisplayName(node.ID), "Node identity and reported network state.") + `<div class="pf-action-row"><a class="pf-text-link" data-node-navigation="` + parent + `" href="` + parent + `">Back to ` + label + `</a></div>` + `<section class="pf-surface"><h2>Identity</h2><p>Internal ID: <code>` + safeText(node.ID) + `</code></p><p>Tailscale hostname: <code>` + safeText(node.Hostname) + `</code></p></section>`
	if requestAccess(r).Admin && node.Mode != "ingress" {
		b += `<section class="pf-surface"><h2>Display name</h2>` + renameForm("nodes", node.ID, node.Name) + `<p class="pf-help">Renaming does not change the Tailscale hostname, identity or routing.</p></section>` + renameScript
	}
	if node.Mode == "loadbalancer" {
		var endpoints struct {
			PublicIPv4 []string `json:"public_ipv4"`
			CheckedAt  int64    `json:"checked_at"`
		}
		_ = c.Store.GetJSON("node_endpoints:"+node.ID, &endpoints)
		b += `<section class="pf-surface"><h2>Load-balancer DNS</h2><p>Public hostname: <code>` + safeText(d.LoadBalancerHostname) + `</code></p><p>Tailscale public IPv4 endpoints: <code>` + safeText(strings.Join(endpoints.PublicIPv4, ", ")) + `</code></p><p class="pf-help">Control fetches this enrolled device’s endpoints from Tailscale. Exactly one distinct public IPv4 is required. Missing or ambiguous endpoints preserve the previous owned A record. No manual override or node-reported address is used.</p></section>`

	}
	if node.Mode == "loadbalancer" {
		var peers struct {
			Tags       []string `json:"tags"`
			ReceivedAt int64    `json:"received_at"`
		}
		_ = c.Store.GetJSON("node_peers:"+node.ID, &peers)
		b += `<section class="pf-surface"><h2>Tailnet backend access</h2><p>Effective tags: <code>` + safeText(strings.Join(peers.Tags, ", ")) + `</code></p><p class="pf-help">Assign additional tags and grants in Tailscale. Local Funnel does not modify ACLs. Backend visibility is not a successful connection test.</p>`
		if requestAccess(r).Admin && !node.Revoked {
			b += `<a class="pf-button" href="/tailnet-exposures?loadbalancer=` + safeText(node.ID) + `">Expose a tailnet machine</a>`
		}
		b += `</section>`
	}

	render(w, "Node settings", b)
}
func validateNodeDeployment(d nodeDeployment, mode string) error {
	if mode == "loadbalancer" && policy.ValidateHostname(d.LoadBalancerHostname) != nil {
		return fmt.Errorf("A valid load-balancer DNS hostname is required")
	}
	if d.CertEmail != "" {
		if address, err := mail.ParseAddress(d.CertEmail); err != nil || address.Address != d.CertEmail {
			return fmt.Errorf("Invalid certificate email")
		}
	}

	if mode == "loadbalancer" && (d.LabelKeys != "" || d.DenyNetworks != "") {
		return fmt.Errorf("Docker settings apply only to ingress")
	}
	if mode == "ingress" && d.CertEmail != "" {
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
