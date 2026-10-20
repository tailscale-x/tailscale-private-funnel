package tsconnection

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/net/tsaddr"
	"tailscale.com/tailcfg"
)

// ServiceDiagnostic contains only selected routing metadata, never a raw netmap,
// private keys, credentials, user profiles, or unrelated peers.
type ServiceDiagnostic struct {
	At         time.Time               `json:"at"`
	Target     string                  `json:"target"`
	DeviceID   string                  `json:"device_id"`
	Candidates []ServiceRouteCandidate `json:"candidates"`
	MapError   string                  `json:"map_error,omitempty"`
	PlanAt     time.Time               `json:"plan_at"`
	Plan       string                  `json:"plan"`
	Connected  bool                    `json:"connected"`
	DialError  string                  `json:"dial_error,omitempty"`
	PingAt     time.Time               `json:"ping_at"`
	Ping       *ipnstate.PingResult    `json:"ping,omitempty"`
	PingError  string                  `json:"ping_error,omitempty"`
}
type ServiceRouteCandidate struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Allowed  bool   `json:"allowed"`
	Primary  bool   `json:"primary"`
}
type diagnosticNode struct {
	StableID      string
	Name          string
	Addresses     []netip.Prefix
	AllowedIPs    []netip.Prefix
	PrimaryRoutes []netip.Prefix
}
type diagnosticMap struct{ Peers []diagnosticNode }

func diagnosticCandidates(m diagnosticMap, ip netip.Addr) []ServiceRouteCandidate {
	var out []ServiceRouteCandidate
	for _, n := range m.Peers {
		c := ServiceRouteCandidate{DeviceID: n.StableID, Name: n.Name}
		allowed := n.AllowedIPs
		if allowed == nil {
			allowed = n.Addresses
		}
		for _, p := range allowed {
			c.Allowed = c.Allowed || p.Contains(ip)
		}
		for _, p := range n.PrimaryRoutes {
			c.Primary = c.Primary || p.Contains(ip)
		}
		if c.Allowed || c.Primary {
			out = append(out, c)
		}
	}
	return out
}

// probeLocalDial uses the exact running SDK's UserDialPlan via LocalAPI. A
// Dial-Self response is reported, NEVER followed with a host-network connection.
func probeLocalDial(ctx context.Context, ip netip.Addr, port uint16, request func(*http.Request) (*http.Response, error)) (string, bool, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://local-tailscaled.sock/localapi/v0/dial", nil)
	if err != nil {
		return "unknown", false, err
	}
	r.Header.Set("Connection", "upgrade")
	r.Header.Set("Upgrade", "ts-dial")
	r.Header.Set("Dial-Host", ip.String())
	r.Header.Set("Dial-Port", strconv.Itoa(int(port)))
	r.Header.Set("Dial-Network", "tcp")
	res, err := request(r)
	if err != nil {
		return "unknown", false, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK && res.Header.Get("Dial-Self") == "true" {
		return "host-fallback-refused", false, nil
	}
	if res.StatusCode == http.StatusSwitchingProtocols {
		return "tailscale", true, nil
	}
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
	if res.StatusCode == http.StatusBadGateway && strings.HasPrefix(string(body), "dial failure:") {
		return "tailscale", false, fmt.Errorf("%s", strings.TrimSpace(string(body)))
	}
	return "unknown", false, fmt.Errorf("selected LocalAPI dial returned HTTP %d", res.StatusCode)
}

func InspectService(ctx context.Context, c Connection, ip netip.Addr, port uint16) (ServiceDiagnostic, error) {
	out := ServiceDiagnostic{At: time.Now().UTC(), Target: net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))}
	if !ip.IsValid() || !tsaddr.IsTailscaleIP(ip) || port == 0 {
		return out, fmt.Errorf("literal Tailscale IP and nonzero TCP port required")
	}
	api, err := c.LocalClient()
	if err != nil {
		return out, err
	}
	out.DeviceID, out.Candidates, err = readServiceMap(ctx, api, ip)
	if err != nil {
		out.MapError = err.Error()
	}
	out.PlanAt = time.Now().UTC()
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	out.Plan, out.Connected, err = probeLocalDial(probe, ip, port, api.DoLocalRequest)
	cancel()
	if err != nil {
		out.DialError = err.Error()
	}
	out.PingAt = time.Now().UTC()
	probe, cancel = context.WithTimeout(ctx, 3*time.Second)
	out.Ping, err = api.Ping(probe, ip, tailcfg.PingDisco)
	cancel()
	if err != nil {
		out.PingError = err.Error()
	}
	return out, nil
}

// Share the redacted metadata extraction between explicit diagnostics and
// passive failure logging. Never serialize the debug result itself.
func readServiceMap(ctx context.Context, api *local.Client, ip netip.Addr) (string, []ServiceRouteCandidate, error) {
	st, err := api.Status(ctx)
	if err != nil {
		return "", nil, err
	}
	device := ""
	if st.Self != nil {
		device = string(st.Self.ID)
	}
	m, err := local.GetDebugResultJSON[diagnosticMap](ctx, api, "current-netmap")
	if err != nil {
		return device, nil, err
	}
	return device, diagnosticCandidates(m, ip), nil
}
