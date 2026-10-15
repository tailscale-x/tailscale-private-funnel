//go:build caddy

package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	caddyfilepkg "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func httpSite(host string, rules []HTTPRule) (string, error) {
	if strings.ContainsAny(host, " \n\r{}\t") || host == "" {
		return "", fmt.Errorf("invalid host")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n%s {\n tls {\n on_demand\n }\n route {\n request_header -Forwarded\n request_header -X-Forwarded-*\n request_header -X-Real-IP\n", host)
	counter := 0
	for _, r := range rules {
		if e := ValidateHTTPRule(r, true); e != nil {
			return "", e
		}
		matches := r.Matches
		if len(matches) == 0 {
			matches = []Match{{Path: "/"}}
		}
		for _, m := range matches {
			counter++
			name := fmt.Sprintf("rule%d", counter)
			fmt.Fprintf(&b, "@%s {\n", name)
			encoded, _ := json.Marshal(m)
			fmt.Fprintf(&b, "funnel_match %s\n", base64.RawStdEncoding.EncodeToString(encoded))
			fmt.Fprintf(&b, "}\nhandle @%s {\n", name)
			if duration, _ := time.ParseDuration(r.RequestTimeout); duration > 0 {
				fmt.Fprintf(&b, "funnel_deadline %s\n", r.RequestTimeout)
			}
			if r.MaxRequestBodyBytes > 0 {
				fmt.Fprintf(&b, "request_body {\nmax_size %d\n}\n", r.MaxRequestBodyBytes)
			}
			if r.Redirect != nil {
				writeHeaders(&b, "header", r.ResponseHeaders)
				d := r.Redirect
				scheme := d.Scheme
				if scheme == "" {
					scheme = "{scheme}"
				}
				h := d.Host
				if h == "" {
					h = "{host}"
				}
				if d.Port != 0 {
					h = net.JoinHostPort(h, strconv.Itoa(int(d.Port)))
				}
				fmt.Fprintf(&b, "redir %s %d\n", caddyToken(scheme+"://"+h+"{uri}"), d.Status)
			} else {
				var upstreams []string
				for _, backend := range r.Backends {
					for n := 0; n < backend.Weight; n++ {
						upstreams = append(upstreams, net.JoinHostPort(backend.Service, strconv.Itoa(int(backend.Port))))
					}
				}
				if len(upstreams) > 32000 {
					return "", fmt.Errorf("too many weighted targets")
				}
				fmt.Fprintf(&b, "reverse_proxy %s {\nheader_up X-Forwarded-Port {http.request.local.port}\nheader_up X-Real-IP {http.request.remote.host}\n", strings.Join(upstreams, " "))
				writeHeaders(&b, "header_up", r.RequestHeaders)
				writeHeaders(&b, "header_down", r.ResponseHeaders)
				b.WriteString("transport funnel_tsnet {\n")
				if r.BackendRequestTimeout != "" {
					fmt.Fprintf(&b, "backend_request_timeout %s\n", r.BackendRequestTimeout)
				}
				if r.BackendTimeout != "" {
					fmt.Fprintf(&b, "response_header_timeout %s\n", r.BackendTimeout)
				}
				b.WriteString("}\n}\n")
			}
			b.WriteString("}\n")
		}
	}
	b.WriteString("handle {\nrespond 404\n}\n}\n}\n")
	return b.String(), nil
}
func sortedKeys(v map[string]string) []string {
	keys := []string{}
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func writeHeaders(b *strings.Builder, directive string, f HeaderFilter) {
	for _, k := range sortedKeys(f.Set) {
		fmt.Fprintf(b, "%s %s %s\n", directive, caddyToken(k), caddyToken(f.Set[k]))
	}
	for _, k := range sortedKeys(f.Add) {
		fmt.Fprintf(b, "%s %s %s\n", directive, caddyToken("+"+k), caddyToken(f.Add[k]))
	}
	for _, k := range f.Remove {
		fmt.Fprintf(b, "%s %s\n", directive, caddyToken("-"+k))
	}
}

// Deadline cancels the upstream context without adding a second proxy hop.
type Deadline struct {
	Duration caddy.Duration `json:"duration"`
}

func (Deadline) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.handlers.funnel_deadline", New: func() caddy.Module { return new(Deadline) }}
}
func (d Deadline) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(d.Duration))
	defer cancel()
	return next.ServeHTTP(w, r.WithContext(ctx))
}
func (d *Deadline) UnmarshalCaddyfile(dispenser *caddyfilepkg.Dispenser) error {
	for dispenser.Next() {
		if !dispenser.NextArg() {
			return dispenser.ArgErr()
		}
		duration, e := time.ParseDuration(dispenser.Val())
		if e != nil {
			return e
		}
		d.Duration = caddy.Duration(duration)
	}
	return nil
}
func init() {
	caddy.RegisterModule(Deadline{})
	httpcaddyfile.RegisterDirectiveOrder("funnel_deadline", httpcaddyfile.Before, "reverse_proxy")
	httpcaddyfile.RegisterHandlerDirective("funnel_deadline", func(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
		var d Deadline
		return &d, d.UnmarshalCaddyfile(h.Dispenser)
	})
}

// ExactMatch preserves Gateway API exact header/query semantics, including
// literal wildcard characters that Caddy's standard matchers treat specially.
type ExactMatch struct {
	MatchConfig Match `json:"match"`
}

func (ExactMatch) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.matchers.funnel_match", New: func() caddy.Module { return new(ExactMatch) }}
}
func (m ExactMatch) Match(r *http.Request) bool {
	v := m.MatchConfig
	if v.Method != "" && r.Method != v.Method {
		return false
	}
	if v.Path != "" {
		path := strings.TrimSuffix(v.Path, "/")
		if v.Exact {
			if r.URL.Path != v.Path {
				return false
			}
		} else if path != "" && r.URL.Path != path && !strings.HasPrefix(r.URL.Path, path+"/") {
			return false
		}
	}
	for k, value := range v.Headers {
		matched := false
		for _, got := range r.Header.Values(k) {
			matched = matched || got == value
		}
		if !matched {
			return false
		}
	}
	for k, value := range v.Query {
		matched := false
		for _, got := range r.URL.Query()[k] {
			matched = matched || got == value
		}
		if !matched {
			return false
		}
	}
	return true
}
func (m *ExactMatch) UnmarshalCaddyfile(d *caddyfilepkg.Dispenser) error {
	d.Next()
	if !d.NextArg() {
		return d.ArgErr()
	}
	raw, err := base64.RawStdEncoding.DecodeString(d.Val())
	if err != nil {
		return err
	}
	if d.NextArg() {
		return d.ArgErr()
	}
	return json.Unmarshal(raw, &m.MatchConfig)
}
func init() { caddy.RegisterModule(ExactMatch{}) }
