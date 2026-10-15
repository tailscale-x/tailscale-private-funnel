package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func gatewayTitle(mode string) string {
	return map[string]string{"docker-gateway": "Docker gateway", "tailnet-gateway": "Tailnet gateway", "k8s-gateway": "Kubernetes gateway", "k8s-forwarder": "Kubernetes forwarder"}[mode]
}
func gatewayHelp(mode string) string {
	switch mode {
	case "docker-gateway":
		return "Discover labeled HTTP containers. The native supervisor needs Docker permissions; isolated forwarding relays receive no Docker socket or Tailscale credentials. Targets require user-defined networks. Local deny patterns exclude sensitive networks."
	case "tailnet-gateway":
		return "Expose HTTP ports on visible tailnet machines. No Docker is required. Assign additional tags and grants in Tailscale; visibility alone does not prove connectivity. Control never modifies ACLs."
	case "k8s-gateway":
		return "Watch Gateway API and Ingress resources inside your cluster. Helm deploys one controller Deployment and node-local forwarders as a DaemonSet. Identity persists in Kubernetes Secrets; no Docker socket, TUN or host mounts. Separate classes and namespaces connect to separate Funnel controls. Public TLS remains Caddy-managed."
	}
	return ""
}
func (c *Control) gateways(w http.ResponseWriter, r *http.Request) {
	nodes, err := c.Store.Nodes("gateways")
	if err != nil {
		http.Error(w, "Unable to read gateways", 503)
		return
	}
	var b strings.Builder
	b.WriteString(pageIntro("Infrastructure", "Gateways", "Docker, tailnet and Kubernetes gateways share persistent Tailscale identity, signed reporting and policy-controlled exposure. Control assigns load balancers automatically."))
	if requestAccess(r).Admin {
		b.WriteString(`<div class="pf-action-row"><a class="pf-button" href="/gateways/new">Add gateway</a></div>`)
	}
	b.WriteString(`<form class="pf-action-row" method="get"><label>Type <select class="pf-input" name="type" onchange="this.form.submit()"><option value="">All gateway types</option>`)
	for _, mode := range []string{"docker-gateway", "tailnet-gateway", "k8s-gateway"} {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, mode, selectedAttr(r.URL.Query().Get("type") == mode), gatewayTitle(mode))
	}
	b.WriteString(`</select></label></form><section class="pf-surface"><div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Name / hostname</th><th>Type</th><th>Health</th><th>Last report</th><th>Exposures</th><th>Actions</th></tr></thead><tbody>`)
	exposures := c.authorizedReports(c.loadExposurePolicy())
	count := 0
	for _, item := range nodes {
		mode := fmt.Sprint(item["mode"])
		if filter := r.URL.Query().Get("type"); filter != "" && filter != mode {
			continue
		}
		id := fmt.Sprint(item["id"])
		node, e := c.Store.Node(id)
		if e != nil {
			continue
		}
		count++
		status := "Waiting for first report"
		if node.Revoked {
			status = "Revoked"
		} else if node.LastReport > time.Now().Add(-5*time.Minute).Unix() {
			status = "Online"
		} else if node.LastReport > 0 {
			status = "Report expired"
		}
		last := "Not yet"
		if node.LastReport > 0 {
			last = time.Unix(node.LastReport, 0).UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(&b, `<tr><td data-label="Name"><strong>%s</strong><small class="pf-muted">%s</small></td><td data-label="Type">%s</td><td data-label="Health">%s</td><td data-label="Last report">%s</td><td data-label="Exposures">%d</td><td data-label="Actions"><a class="pf-text-link" href="/nodes/%s/settings">Node details</a></td></tr>`, safeText(node.Name), safeText(node.Hostname), gatewayTitle(mode), status, last, len(exposures[id]), safeText(id))
	}
	if count == 0 {
		b.WriteString(`<tr><td colspan="6"><div class="pf-empty"><strong>No gateways yet</strong><p>Add a gateway, create an exposure policy, then publish a backend.</p></div></td></tr>`)
	}
	b.WriteString(`</tbody></table></div></section>`)
	render(w, "Gateways", b.String())
}
func (c *Control) addGateway(w http.ResponseWriter, r *http.Request) {
	if !requestAccess(r).Admin {
		http.Error(w, "Administrator required", 403)
		return
	}
	var b strings.Builder
	b.WriteString(pageIntro("Infrastructure", "Add gateway", "Step 1 of 4 · Choose how this node discovers HTTP backends."))
	for _, mode := range []string{"docker-gateway", "tailnet-gateway", "k8s-gateway"} {
		fmt.Fprintf(&b, `<section class="pf-surface pf-form-surface"><h2>%s</h2><p class="pf-subtitle">%s</p><a class="pf-button" href="/enroll/%s">Choose %s</a></section>`, gatewayTitle(mode), gatewayHelp(mode), mode, gatewayTitle(mode))
	}
	render(w, "Add gateway", b.String())
}
func (c *Control) gatewayEnrollment(w http.ResponseWriter, r *http.Request) {
	mode := chi.URLParam(r, "mode")
	if !store.IsGateway(mode) {
		http.NotFound(w, r)
		return
	}
	if !requestAccess(r).Admin {
		http.Error(w, "Administrator required", 403)
		return
	}
	fields := `<label class="pf-field"><span class="pf-label">Name</span><input class="pf-input" name="name" required maxlength="128"></label><label class="pf-field"><span class="pf-label">Node hostname (optional)</span><input class="pf-input" name="hostname" placeholder="Target hostname + -` + mode + `"></label>`
	if mode == "k8s-gateway" {
		fields += `<label class="pf-field"><span class="pf-label">Installation name</span><input class="pf-input" name="installation_name" placeholder="main"><small>Choose a different installation and namespace for each Funnel control.</small></label><label class="pf-field"><span class="pf-label">Watched namespaces</span><input class="pf-input" name="watch_namespaces" placeholder="voice,apps"><small>Empty watches all namespaces; RBAC follows this selection.</small></label><label class="pf-field"><span class="pf-label">Forwarder node selector (JSON)</span><textarea class="pf-input" name="node_selector" placeholder='{"kubernetes.io/arch":"amd64"}'></textarea><small>Only selected nodes run a forwarder. Traffic stays on that node.</small></label><label class="pf-field"><span class="pf-label">Namespace (optional)</span><input class="pf-input" name="kubernetes_namespace" placeholder="tailscale-private-funnel-main-system"></label><label class="pf-field"><span class="pf-label">GatewayClass (optional)</span><input class="pf-input" name="gateway_class" placeholder="tailscale-private-funnel"></label><label class="pf-field"><span class="pf-label">IngressClass (optional)</span><input class="pf-input" name="ingress_class" placeholder="tailscale-private-funnel"></label>`
	}
	if mode != "k8s-gateway" {
		fields += `<label class="pf-field"><span class="pf-label">State directory (optional)</span><input class="pf-input" name="state_dir" placeholder="/var/lib/tailscale-private-funnel/` + mode + `/state"></label><label class="pf-field"><span class="pf-label">Data directory (optional)</span><input class="pf-input" name="data_dir" placeholder="/var/lib/tailscale-private-funnel/` + mode + `/data"></label>`
	}
	if mode == "docker-gateway" {
		fields += `<label class="pf-field"><span class="pf-label">Additional label keys (optional)</span><input class="pf-input" name="label_keys"></label><label class="pf-field"><span class="pf-label">Local network deny patterns (optional)</span><input class="pf-input" name="deny_networks"></label>`
	}
	b := pageIntro("Infrastructure", "Add "+gatewayTitle(mode), "Step 2 of 4 · Configure the node. Control selects load balancers automatically.") + `<section class="pf-surface pf-form-surface"><h2>Prerequisites</h2><p class="pf-subtitle">` + gatewayHelp(mode) + `</p><p class="pf-help">Bootstrap expires after 15 minutes and can be used once. Identity persists across reboot. Reset and control revocation are explicit. OAuth stays server-side.</p></section><section class="pf-surface pf-form-surface"><h2>Configure</h2><form id="gateway-enrollment" class="pf-form-grid">` + fields + `<div class="pf-form-actions"><button class="pf-button">Generate installation instructions</button></div></form><p id="gateway-error" role="alert"></p></section><section class="pf-surface pf-form-surface" id="gateway-install" hidden><h2>Step 3 · Install</h2><p id="gateway-instructions" class="pf-subtitle"></p><button class="pf-secondary-button" id="copy-gateway" type="button">Copy installation script</button><pre class="pf-command" id="gateway-script"></pre><h3>Publish your first backend</h3><pre class="pf-command" id="gateway-example"></pre><p class="pf-help">State and application data must stay persistent. Use status to diagnose, replace the binary or image to update, and retain the same paths when rolling back. Reset deletes local identity; revoke the node through control to withdraw its Services and owned DNS.</p></section><section class="pf-surface pf-form-surface" id="gateway-verify" hidden><h2>Step 4 · Verify</h2><p role="status" id="gateway-state">Waiting for enrollment and a healthy signed report…</p><a class="pf-text-link" id="gateway-detail" href="/gateways">Open node details</a><p><a class="pf-text-link" href="/policies">Configure exposure policy</a></p></section><p><a class="pf-text-link" href="/gateways/new">Choose another gateway type</a></p><script src="/assets/gateway-enrollment.js" defer data-mode="` + mode + `"></script>`
	render(w, "Add "+gatewayTitle(mode), b)
}

func (c *Control) nodeGet(w http.ResponseWriter, r *http.Request) {
	n, err := c.Store.Node(chi.URLParam(r, "id"))
	if err != nil {
		for _, mode := range []string{"docker-gateway", "tailnet-gateway", "k8s-gateway", "loadbalancer"} {
			items, e := c.Store.Enrollments(mode)
			if e != nil {
				http.Error(w, "Unable to read enrollment", 503)
				return
			}
			for _, item := range items {
				if item.ID == chi.URLParam(r, "id") {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusAccepted)
					writeJSON(w, map[string]any{"id": item.ID, "mode": item.Mode, "state": "enrollment_pending", "revoked": item.Revoked, "last_report": 0})
					return
				}
			}
		}
		http.NotFound(w, r)
		return
	}
	writeJSON(w, n)
}

func (c *Control) nodeCleanup(w http.ResponseWriter, r *http.Request) {
	n, err := c.Store.Node(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !n.Revoked {
		http.Error(w, "revoke this node before cleanup", 409)
		return
	}
	err = c.cleanupRevokedNode(r.Context(), n)
	writeJSON(w, map[string]any{"pending": err != nil, "message": fmt.Sprint(err)})
}
