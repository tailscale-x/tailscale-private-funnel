package tsconnection

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestLocalServiceDiagnosticNeverFollowsHostFallback(t *testing.T) {
	for _, test := range []struct {
		name             string
		status           int
		self, body, want string
		connected        bool
	}{
		{"missing route", 200, "true", "", "host-fallback-refused", false},
		{"connected", 101, "", "", "tailscale", true},
		{"backend timeout", 502, "", "dial failure: i/o timeout", "tailscale", false},
		{"unknown", 500, "", "", "unknown", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			plan, connected, _ := probeLocalDial(t.Context(), netip.MustParseAddr("100.112.216.176"), 80, func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/localapi/v0/dial" || r.Header.Get("Dial-Host") != "100.112.216.176" || r.Header.Get("Upgrade") != "ts-dial" {
					t.Fatal("unexpected LocalAPI request")
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Dial-Self": []string{test.self}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			if plan != test.want || connected != test.connected || calls != 1 {
				t.Fatal(plan, connected, calls)
			}
		})
	}
}

func TestServiceDiagnosticsRejectHostNetworking(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "192.168.1.1", "8.8.8.8", "::1"} {
		if _, err := InspectService(context.Background(), nil, netip.MustParseAddr(ip), 80); err == nil {
			t.Fatal(ip)
		}
	}
}

func TestServiceDiagnosticDistinguishesAdvertisedAndPrimaryRoutes(t *testing.T) {
	var m diagnosticMap
	raw := `{"PrivateKey":"secret","UserProfiles":{"secret":"user"},"Peers":[{"StableID":"old","Name":"old","AllowedIPs":["100.112.216.176/32"],"PrimaryRoutes":[],"PrivateKey":"secret"},{"StableID":"current","Name":"current","AllowedIPs":["100.112.216.176/32"],"PrimaryRoutes":["100.112.216.176/32"]},{"StableID":"unrelated","Name":"other","Addresses":["100.64.0.99/32"]}]}`
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	got := diagnosticCandidates(m, netip.MustParseAddr("100.112.216.176"))
	if len(got) != 2 || !got[0].Allowed || got[0].Primary || !got[1].Primary {
		t.Fatal(got)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "unrelated") {
		t.Fatal("unrelated/private metadata leaked")
	}
}
