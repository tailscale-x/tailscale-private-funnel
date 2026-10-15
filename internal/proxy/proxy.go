//go:build caddy

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	"github.com/caddyserver/certmagic"
)

var reloadState struct {
	sync.Mutex
	listen, email, dataDir, tailscaleBlock string
}

// Start runs the public load balancer. Gateway owns Docker discovery and
// forwards approved Services directly to their selected backends.
func Start(mode, listen string, dial func(context.Context, string, string) (net.Conn, error)) error {
	sharedDialer.Lock()
	sharedDialer.dial = dial
	sharedDialer.Unlock()
	if mode != "loadbalancer" {
		return fmt.Errorf("Caddy is available only in loadbalancer mode")
	}
	if listen == "" {
		listen = ":443"
	}
	dataDir := os.Getenv("FUNNEL_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/tailscale-private-funnel/data"
	}
	if err := configureStorage(dataDir); err != nil {
		return err
	}
	email := os.Getenv("FUNNEL_CERT_EMAIL")
	tailscaleBlock := ""
	routes, err := routesFromEnv()
	if err != nil {
		return err
	}
	config, err := caddyfile(listen, email, dataDir, tailscaleBlock, routes)
	if err != nil {
		return err
	}
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return fmt.Errorf("caddyfile adapter is unavailable")
	}
	b, warnings, err := adapter.Adapt([]byte(config), nil)
	if err != nil {
		return fmt.Errorf("adapt embedded Caddy config: %w", err)
	}
	_ = warnings
	if err := caddy.Load(b, false); err != nil {
		return fmt.Errorf("load embedded Caddy config: %w", err)
	}
	reloadState.Lock()
	reloadState.listen, reloadState.email, reloadState.dataDir, reloadState.tailscaleBlock = listen, email, dataDir, tailscaleBlock
	reloadState.Unlock()
	return nil
}

// ReloadRoutes replaces only the generated host-to-Service routes. Caddy's
// storage and the Tailscale transport state remain on disk and survive reload.
func ReloadRoutes(routes []Route) error {
	reloadState.Lock()
	defer reloadState.Unlock()
	config, err := caddyfile(reloadState.listen, reloadState.email, reloadState.dataDir, reloadState.tailscaleBlock, routes)
	if err != nil {
		return err
	}
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return fmt.Errorf("caddyfile adapter is unavailable")
	}
	b, _, err := adapter.Adapt([]byte(config), nil)
	if err != nil {
		return err
	}
	return caddy.Load(b, false)
}

func configureStorage(dataDir string) error {
	for _, path := range []string{filepath.Join(dataDir, "caddy"), filepath.Join(dataDir, "config", "caddy")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			return fmt.Errorf("create Caddy directory: %w", err)
		}
	}
	_ = os.Setenv("XDG_DATA_HOME", dataDir)
	_ = os.Setenv("XDG_CONFIG_HOME", filepath.Join(dataDir, "config"))
	if os.Getenv("HOME") == "" {
		_ = os.Setenv("HOME", dataDir)
	}
	caddy.DefaultStorage = &certmagic.FileStorage{Path: filepath.Join(dataDir, "caddy")}
	caddy.ConfigAutosavePath = filepath.Join(dataDir, "config", "caddy", "autosave.json")
	return nil
}

func caddyfile(listen, email, dataDir, tailscaleBlock string, routes []Route) (string, error) {
	var sites strings.Builder
	grouped := map[string][]HTTPRule{}
	simple := []Route{}
	for _, route := range routes {
		if len(route.Rules) > 0 {
			grouped[route.Hostname] = append(grouped[route.Hostname], route.Rules...)
		} else {
			simple = append(simple, route)
		}
	}
	hosts := []string{}
	for host := range grouped {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		rules := grouped[host]
		text, err := httpSite(host, rules)
		if err != nil {
			return "", err
		}
		sites.WriteString(text)
	}
	routes = simple
	for _, route := range routes {
		if err := validateRoute(route); err != nil {
			return "", err
		}
		transport := "transport funnel_tsnet"
		upstream := net.JoinHostPort(route.Service, strconv.Itoa(int(route.Port)))
		fmt.Fprintf(&sites, "\n%s {\n\ttls {\n\t\ton_demand\n\t}\n\trequest_header -Forwarded\n\trequest_header -X-Forwarded-*\n\trequest_header -X-Real-IP\n\treverse_proxy %s {\n\t\theader_up X-Forwarded-Port {http.request.local.port}\n\t\theader_up X-Real-IP {http.request.remote.host}\n\t\t%s\n\t}\n}\n", route.Hostname, upstream, transport)
	}
	// Keep an explicit catch-all for hosts that have no approved application;
	// on-demand TLS still uses the local always-allow permission endpoint, but
	// the request receives 404 until control supplies a route.
	sites.WriteString("\n:443 {\n\ttls {\n\t\ton_demand\n\t}\n\trespond 404\n}\n")
	emailOption := ""
	if email != "" {
		emailOption = "email " + caddyToken(email)
	}
	return fmt.Sprintf(`{
	%s
	storage file_system %s
%s
	on_demand_tls {
		ask http://127.0.0.1:9123/
	}
}

http://127.0.0.1:9123 {
	respond 200
}
:80 {
	redir https://{host}{uri} permanent
}
%s
`, emailOption, caddyToken(filepath.Join(dataDir, "caddy")), tailscaleBlock, sites.String()), nil
}

func validateRoute(route Route) error {
	if route.Hostname == "" || strings.ContainsAny(route.Hostname, " \t\r\n{}") {
		return fmt.Errorf("invalid route hostname %q", route.Hostname)
	}
	if route.Port == 0 || route.Service == "" || strings.ContainsAny(route.Service, " \t\r\n{}") {
		return fmt.Errorf("invalid Service route for %q", route.Hostname)
	}
	if ip, err := netip.ParseAddr(strings.Trim(route.Service, "[]")); err != nil || !ip.IsValid() {
		if strings.ContainsAny(route.Service, ":/") || strings.TrimSuffix(route.Service, ".") == "" {
			return fmt.Errorf("invalid Service target for %q", route.Hostname)
		}
	}
	return nil
}

func routesFromEnv() ([]Route, error) {
	value := strings.TrimSpace(os.Getenv("FUNNEL_ROUTES"))
	if value == "" {
		return nil, nil
	}
	var routes []Route
	if err := json.Unmarshal([]byte(value), &routes); err != nil {
		return nil, fmt.Errorf("parse FUNNEL_ROUTES: %w", err)
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Hostname < routes[j].Hostname })
	return routes, nil
}

func caddyToken(value string) string {
	return strconv.Quote(value)
}

// ValidateConfig adapts the embedded load balancer configuration without
// starting Caddy. Release CI calls this for syntax/module regressions.
func ValidateConfig(mode string) error {
	if mode != "loadbalancer" {
		return fmt.Errorf("Caddy configuration is defined only for loadbalancer mode")
	}
	config, err := caddyfile(":443", "admin@example.invalid", "/tmp/private-funnel", "", []Route{{Hostname: "app.example.test", Service: "svc-app", Port: 8080}})
	if err != nil {
		return err
	}
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		return fmt.Errorf("caddyfile adapter is unavailable")
	}
	_, _, err = adapter.Adapt([]byte(config), nil)
	return err
}

// Stop drains public listeners without closing the application-owned tsnet identity.
func Stop() error { return caddy.Stop() }
