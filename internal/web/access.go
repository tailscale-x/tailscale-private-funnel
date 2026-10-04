package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
)

// DefaultCapability uses the deployment's example domain and remains editable
// in the UI. Deployments on another domain must replace it with a domain they
// control in their tailnet policy.
const DefaultCapability = "private-funnel.dlkn.dev/cap/control"

type AccessSettings struct {
	Admins     []string            `json:"admins"`
	Roles      map[string][]string `json:"roles"`
	Capability string              `json:"capability"`
}

type accessKey struct{}
type Access struct {
	Admin    bool
	Operator bool
	Viewer   bool
}

func requestAccess(r *http.Request) Access {
	a, _ := r.Context().Value(accessKey{}).(Access)
	return a
}

func (c *Control) accessSettings() AccessSettings {
	var settings AccessSettings
	if err := c.Store.GetJSON("access_roles", &settings); err != nil {
		_ = c.Store.GetJSON("tailnet", &settings)
	}
	if settings.Capability == "" {
		settings.Capability = DefaultCapability
	}
	if settings.Roles == nil {
		settings.Roles = map[string][]string{}
	}
	return settings
}

func (settings AccessSettings) permissions(p Principal) Access {
	a := Access{Admin: exactAdmin(p, settings.Admins) || hasRole(p, settings.Roles, "admin")}
	a.Operator = a.Admin || hasRole(p, settings.Roles, "operator")
	a.Viewer = a.Operator || hasRole(p, settings.Roles, "viewer")
	// Only WhoIs supplies capabilities. Never accept role or identity headers
	// from the HTTP client, including on the bootstrap listener.
	for _, raw := range p.Capabilities[settings.Capability] {
		var grant struct {
			Roles []string `json:"roles"`
		}
		if json.Unmarshal(raw, &grant) != nil {
			continue
		}
		for _, role := range grant.Roles {
			switch role {
			case "admin":
				a.Admin, a.Operator, a.Viewer = true, true, true
			case "operator":
				a.Operator, a.Viewer = true, true
			case "viewer":
				a.Viewer = true
			}
		}
	}
	return a
}

func (c *Control) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		if p.NodeID == "" {
			http.Error(w, "verified tailnet identity required", 403)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
					http.Error(w, "cross-origin management request rejected", 403)
					return
				}
			}
		}
		if (r.Method == http.MethodPost && (r.URL.Path == "/api/reports" || strings.HasPrefix(r.URL.Path, "/api/enrollments/") && strings.HasSuffix(r.URL.Path, "/signing-key") || strings.HasPrefix(r.URL.Path, "/api/node/"))) || (r.Method == http.MethodGet && r.URL.Path == "/api/node/config") {
			next.ServeHTTP(w, r)
			return
		}
		c.accessMu.Lock()
		settings := c.accessSettings()
		if len(settings.Admins) == 0 && !roleConfigured(settings.Roles) {
			sharedTag := contains(p.Tags, "tag:tailscale-private-funnel")
			if (p.Tagged || len(p.Tags) != 0 || p.Login == "") && !sharedTag {
				c.accessMu.Unlock()
				http.Error(w, "first setup requires an untagged verified user identity", 403)
				return
			}
			if !p.Tagged && len(p.Tags) == 0 && p.Login != "" {
				settings.Admins = []string{p.Login}
			} else {
				settings.Admins = []string{"node:" + p.NodeID}
			}
			if err := c.Store.SetJSON("access_roles", settings); err != nil {
				c.accessMu.Unlock()
				http.Error(w, err.Error(), 500)
				return
			}
			_ = c.Store.Audit("administrator.bootstrap", p.Login)
		}
		c.accessMu.Unlock()
		a := settings.permissions(p)
		level := requiredAccess(r)
		allowed := a.Viewer
		if level == "admin" {
			allowed = a.Admin
		} else if level == "operator" {
			allowed = a.Operator
		}
		if !allowed {
			http.Error(w, "Tailscale identity lacks the required control role: "+level, 403)
			return
		}
		actor := p.Login
		if p.Tagged || actor == "" {
			actor = "node:" + p.NodeID
		}
		r = WithIdentity(r, actor)
		r = r.WithContext(context.WithValue(r.Context(), accessKey{}, a))
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			_ = c.Store.Audit(r.Method+" "+r.URL.Path, actor)
		}
		next.ServeHTTP(w, r)
	})
}

func (c *Control) accessGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"settings": c.accessSettings(), "identity": principal(r), "permissions": requestAccess(r)})
}

func (c *Control) accessSave(w http.ResponseWriter, r *http.Request) {
	var settings AccessSettings
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&settings) != nil {
			http.Error(w, "invalid access settings", 400)
			return
		}
	} else {
		settings.Capability = strings.TrimSpace(r.FormValue("capability"))
		settings.Admins = splitValues(r.FormValue("admins"))
		settings.Roles = map[string][]string{}
		for _, role := range []string{"admin", "operator", "viewer"} {
			settings.Roles[role] = splitValues(r.FormValue(role))
		}
	}
	if settings.Capability == "" {
		settings.Capability = DefaultCapability
	}
	if !strings.Contains(settings.Capability, "/") || strings.HasPrefix(settings.Capability, "tailscale.com/") || strings.HasPrefix(settings.Capability, "tailscale.io/") {
		http.Error(w, "use a custom application capability such as example.com/cap/private-funnel", 400)
		return
	}
	for role, selectors := range settings.Roles {
		if role != "admin" && role != "operator" && role != "viewer" {
			http.Error(w, "unknown control role: "+role, 400)
			return
		}
		for _, selector := range selectors {
			if strings.TrimSpace(selector) == "*" {
				http.Error(w, "wildcard UI roles are not supported; select a user, group, tag or node", 400)
				return
			}
		}
	}
	if !settings.permissions(principal(r)).Admin {
		http.Error(w, "these settings would remove your administrator access; retain a matching admin selector", 409)
		return
	}
	if err := c.Store.SetJSON("access_roles", settings); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("access-roles.save", identityFromRequest(r))
	if r.Header.Get("HX-Request") == "true" {
		fmt.Fprint(w, "Access settings saved.")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Redirect(w, r, "/access", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(settings)
}

func splitValues(value string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' }) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (c *Control) accessPage(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	settings := c.accessSettings()
	join := func(values []string) string { return template.HTMLEscapeString(strings.Join(values, ",")) }
	body := pageIntro("Access", "Access roles", "WhoIs identity is the source of truth. Map verified users, groups, tags, node IDs, or app capabilities to the least privilege role they need.") + fmt.Sprintf(`<div class="pf-alert pf-alert-info"><strong>Your verified identity.</strong> Selectors: <code>%s</code> · Node: <code>%s</code></div><section class="pf-surface pf-form-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Authorization</p><h2>Role mapping</h2></div><span class="pf-pill pf-pill-neutral">viewer · operator · admin</span></div><p class="pf-muted">Viewer reads the UI. Operator can preview and execute ownership-scoped DNS syncs. Admin manages settings, providers, zones, enrollments, access rules, and policies.</p><form class="pf-form-grid" method="post" action="/api/access" hx-post="/api/access" hx-target="#access-result"><label class="pf-field"><span class="pf-label">Application capability</span><input class="pf-input" name="capability" value="%s" required></label><label class="pf-field"><span class="pf-label">Exact administrator identities</span><input class="pf-input" name="admins" value="%s" placeholder="alice@example.com,group:platform-admins,tag:control"></label><label class="pf-field"><span class="pf-label">Admin selectors</span><input class="pf-input" name="admin" value="%s" placeholder="group:platform-admins,tag:control"></label><label class="pf-field"><span class="pf-label">Operator selectors</span><input class="pf-input" name="operator" value="%s" placeholder="group:platform-operators,tag:operator"></label><label class="pf-field"><span class="pf-label">Viewer selectors</span><input class="pf-input" name="viewer" value="%s" placeholder="group:platform-readonly"></label><div class="pf-form-actions"><button class="pf-button" type="submit">Save access rules <span aria-hidden="true">→</span></button><span id="access-result" class="pf-inline-result" role="status"></span></div></form></section><section class="pf-surface"><div class="pf-section-heading"><div><p class="pf-kicker">Identity model</p><h2>How access is decided</h2></div></div><p class="pf-muted">Tagged service nodes use their tags and stable node ID. Human users use their login and WhoIs groups. Tailscale account Owner and Admin roles stay in the Tailscale admin console; this app maps only verified application selectors.</p></section>`, template.HTMLEscapeString(strings.Join(p.RoleSelectors(), ", ")), template.HTMLEscapeString(p.NodeID), template.HTMLEscapeString(settings.Capability), join(settings.Admins), join(settings.Roles["admin"]), join(settings.Roles["operator"]), join(settings.Roles["viewer"]))
	render(w, "Access & roles", body)
}
