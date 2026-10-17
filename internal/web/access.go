package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func (c *Control) loadAccessSettings() (AccessSettings, error) {
	var settings AccessSettings
	if err := c.Store.GetJSON("access_roles", &settings); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return settings, err
		}
		if err := c.Store.GetJSON("tailnet", &settings); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return settings, err
		}
	}
	if settings.Capability == "" {
		settings.Capability = DefaultCapability
	}
	if settings.Roles == nil {
		settings.Roles = map[string][]string{}
	}
	if _, configured := settings.Roles["admin"]; !configured {
		settings.Roles["admin"] = []string{"autogroup:owner"}
	}
	return settings, nil
}

func (settings AccessSettings) permissions(p Principal) Access {
	adminSelectors, configured := settings.Roles["admin"]
	if !configured {
		adminSelectors = []string{"autogroup:owner"}
	}
	a := Access{Admin: exactAdmin(p, settings.Admins) || hasRole(p, map[string][]string{"admin": adminSelectors}, "admin")}
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
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
					http.Error(w, "cross-origin management request rejected", 403)
					return
				}
			}
		}
		if isRPCPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		if p.NodeID == "" {
			http.Error(w, "verified tailnet identity required", 403)
			return
		}
		p.TailnetRole = c.tailnetRole(r.Context(), p)
		r = WithPrincipal(r, p)
		c.accessMu.Lock()
		settings, settingsErr := c.loadAccessSettings()
		if settingsErr != nil {
			c.accessMu.Unlock()
			http.Error(w, "authorization settings unavailable; retry later", http.StatusServiceUnavailable)
			return
		}

		c.accessMu.Unlock()
		a := settings.permissions(p)
		level := "viewer"
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

func splitValues(value string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '\n' }) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
