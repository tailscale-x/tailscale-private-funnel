package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"tailscale.com/tsnet"
)

var macQAEdge func(*testing.T, func(context.Context, string, string) (net.Conn, error)) (string, func(string, uint16))

// Opt-in real tailnet fixture: no production control/database or DNS provider.
// The external Mac CLI uses its untagged host only to request bootstrap, then
// registers and serves through a separate ephemeral identity.
func TestLiveMacPhoneHome(t *testing.T) {
	dir := os.Getenv("FUNNEL_MAC_QA_DIR")
	if dir == "" || macQAEdge == nil {
		t.Skip("requires Mac runner and protected credentials")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	home, _ := os.UserHomeDir()
	read := func(purpose, name string) string {
		p := filepath.Join(home, ".config/tailscale-private-funnel/credentials/main-"+purpose, name)
		info, e := os.Stat(p)
		if e != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("protected credential required", p)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		return strings.TrimSpace(string(b))
	}
	issuer := tailscale.Client{ClientID: read("issuer", "client-id"), ClientSecret: read("issuer", "client-secret"), Tailnet: "-"}
	services := tailscale.Client{ClientID: read("services", "client-id"), ClientSecret: read("services", "client-secret"), Tailnet: "-"}
	key := func(role identity.Role) string {
		k, e := issuer.CreateWorkloadAuthKey(ctx, string(role), "mac-phonehome-qa", []string{identity.Tag("main", role)}, true)
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	service := fmt.Sprintf("svc:tailscale-private-funnel-mac-qa-%d", time.Now().Unix())
	if _, e := services.CreateService(ctx, tailscale.Service{Name: service, Ports: []string{"tcp:8080"}, Tags: []string{identity.ServiceTag("main", true)}}); e != nil {
		t.Fatal(e)
	}
	cleanupService := func(name string) {
		cleanup, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if e := services.DeleteService(cleanup, name); e != nil {
			t.Error("QA Service cleanup", e)
		}
	}
	defer cleanupService(service)
	server := &tsnet.Server{Dir: filepath.Join(t.TempDir(), "control"), Hostname: "mac-qa-control", AuthKey: key(identity.Control), Ephemeral: true, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
	status, e := server.Up(ctx)
	server.AuthKey = ""
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	defer func() {
		lc, e := server.LocalClient()
		if e == nil {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			_ = lc.Logout(cleanup)
		}
	}()
	url := "http://" + strings.TrimPrefix(service, "svc:") + "." + status.CurrentTailnet.MagicDNSSuffix + ":8080"
	_, st := testControl(t)
	c := registrationTestControl(t, st)
	c.TailscaleClient = nil
	c.BuildVersion = "dev-phonehome-mac"
	installDNSFixture(t, st, "example.com")
	_ = st.SetSecret("tailscale_issuer_client_id", issuer.ClientID)
	_ = st.SetSecret("tailscale_issuer_client_secret", issuer.ClientSecret)
	_ = st.SetSecret("tailscale_services_client_id", services.ClientID)
	_ = st.SetSecret("tailscale_services_client_secret", services.ClientSecret)
	_ = st.SetJSON("tailnet", map[string]string{"name": status.CurrentTailnet.MagicDNSSuffix, "management_url": url})
	_ = st.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Mac QA only", HostnameRegexes: []string{`mac[.]example[.]com`}, Enabled: true}}})
	defer func() {
		apps, _ := st.Applications()
		for _, a := range apps {
			if strings.HasPrefix(a.ID, "local-") {
				cleanupService(a.ServiceName)
			}
		}
	}()
	listener, e := tsconnection.ListenControl(ctx, tsconnection.Wrap(server), service, 8080)
	if e != nil {
		t.Fatal(e)
	}
	lc, _ := server.LocalClient()
	handler := c.Handler()
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, e := lc.WhoIs(r.Context(), r.RemoteAddr)
		if e != nil || who.Node == nil {
			http.Error(w, "verified peer required", 403)
			return
		}
		p := Principal{NodeID: string(who.Node.StableID), Hostname: who.Node.Name, Tags: who.Node.Tags, Tagged: len(who.Node.Tags) > 0}
		for _, addr := range who.Node.Addresses {
			p.Addresses = append(p.Addresses, addr.String())
		}
		if who.UserProfile != nil {
			p.Login = who.UserProfile.LoginName
		}
		handler.ServeHTTP(w, WithPrincipal(r, p))
	})}
	defer httpServer.Close()
	go httpServer.Serve(listener)
	debugLog, err := os.OpenFile(filepath.Join(dir, "lb-debug.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer debugLog.Close()
	lb := &tsnet.Server{Dir: filepath.Join(t.TempDir(), "lb"), Hostname: "mac-qa-client", AuthKey: key(identity.LoadBalancer), Ephemeral: true, Logf: func(format string, args ...any) {
		fmt.Fprintf(debugLog, time.Now().UTC().Format(time.RFC3339)+" "+format+"\n", args...)
	}, UserLogf: func(string, ...any) {}}
	if _, e = lb.Up(ctx); e != nil {
		t.Fatal(e)
	}
	lb.AuthKey = ""
	edgeURL, updateEdge := macQAEdge(t, tsconnection.Wrap(lb).Dial)
	// Caddy and its selected tsnet client must predate the application Service.
	_ = os.WriteFile(filepath.Join(dir, "control-url"), []byte(url), 0600)
	defer lb.Close()
	defer func() {
		lc, e := lb.LocalClient()
		if e == nil {
			cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			_ = lc.Logout(cleanup)
		}
	}()
	client := &http.Client{Timeout: 12 * time.Second}
	var firstApp time.Time

	tested := false
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if _, err := os.Stat(filepath.Join(dir, "abort")); err == nil {
			t.Skip("external QA abort; traffic not accepted")
		}

		installTestEdge(t, st)
		seedEdgeReport(t, st, "edge-id")
		if e := c.reconcileExposures(ctx); e != nil {
			t.Log("fixture reconciliation pending", e)
		}
		apps, _ := st.Applications()
		for _, app := range apps {
			if app.BackendSource != "local" || !app.Enabled {
				continue
			}
			if !tested {
				if firstApp.IsZero() {
					firstApp = time.Now()
					go func(name string) {
						timer := time.NewTicker(time.Second)
						defer timer.Stop()
						for {
							select {
							case <-ctx.Done():
								return
							case <-timer.C:
								svc, err := services.GetService(ctx, name)
								if err != nil {
									continue
								}
								for _, ip := range svc.Addresses {
									if addr, err := netip.ParseAddr(ip); err == nil {
										if _, ok := tsnet.TestHooks.LocalBackend(lb).PeerForIP(addr); ok {
											t.Log("Fresh Service route arrived", time.Since(firstApp))
											return
										}
									}
								}
							}
						}
					}(app.ServiceName)
					updateEdge(app.ServiceFQDN, app.ServicePort)
				}
				if time.Since(firstApp) > 45*time.Second {
					t.Fatal("fresh Service did not become reachable within 45 seconds on the existing Caddy load balancer")
				}
				api, _ := lb.LocalClient()
				wire, _, qe := api.QueryDNS(ctx, app.ServiceFQDN, "A")
				t.Log("Service DNS", app.ServiceFQDN, len(wire), qe)
				svc, se := services.GetService(ctx, app.ServiceName)
				t.Log("Service metadata", svc, se)
				nm := tsnet.TestHooks.LocalBackend(lb).NetMapWithPeers()
				for _, peer := range nm.Peers {
					if strings.Contains(peer.Name(), "tunnel") {
						t.Log("LB tunnel peer", peer.Name(), peer.AllowedIPs().AsSlice())
					}
				}

				if se == nil {
					for _, ip := range svc.Addresses {
						if addr, err := netip.ParseAddr(ip); err == nil {
							peer, ok := tsnet.TestHooks.LocalBackend(lb).PeerForIP(addr)
							t.Log("Service route", ip, ok, peer.Route)
						}
					}
				}
				resp, e := client.Get(edgeURL + "/sse")
				if e != nil {
					t.Log("Service probe pending", e)
					continue
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					t.Log("Caddy upstream pending", resp.StatusCode, time.Since(firstApp))
					continue
				}
				t.Log("First fresh-Service Caddy response", time.Since(firstApp))
				body, e := io.ReadAll(resp.Body)
				resp.Body.Close()
				if e != nil || len(body) != 1040014 || !bytes.HasSuffix(body, []byte("data: [DONE]\n\n")) {
					t.Fatalf("SSE incomplete: %d %v", len(body), e)
				}
				client.Timeout = 80 * time.Second
				ws, _, e := websocket.Dial(ctx, strings.Replace(edgeURL, "http://", "ws://", 1)+"/ws", &websocket.DialOptions{HTTPClient: client})
				if e != nil {
					t.Fatal(e)
				}
				_ = ws.Write(ctx, websocket.MessageText, []byte("mac-phonehome"))
				_, message, e := ws.Read(ctx)
				ws.CloseNow()
				if e != nil || string(message) != "mac-phonehome" {
					t.Fatal("WebSocket", e)
				}
				resp, e = client.Get(edgeURL + "/sse?slow=1")
				if e != nil {
					t.Fatal(e)
				}
				body, e = io.ReadAll(resp.Body)
				resp.Body.Close()
				if e != nil || !bytes.HasSuffix(body, []byte("data: [DONE]\n\n")) {
					t.Fatal("stream across config/report ticks failed", e)
				}
				tested = true
				_ = os.WriteFile(filepath.Join(dir, "traffic-passed"), []byte(app.ServiceName), 0600)
			}
		}
		if tested {
			if _, e := os.Stat(filepath.Join(dir, "stopped")); e == nil {
				// Explicit withdrawal must retire membership and the fixture DNS ledger.
				apps, _ = st.Applications()
				for _, a := range apps {
					if a.BackendSource == "local" && a.Enabled {
						t.Fatal("tunnel not withdrawn")
					}
				}
				if e := c.reconcileExposures(ctx); e != nil {
					t.Fatal(e)
				}
				partitions, e := st.LedgerPartitions()
				if e != nil || len(partitions) != 0 {
					t.Fatal("owned DNS cleanup incomplete", partitions, e)
				}
				t.Log("Mac phone-home, real WhoIs, ephemeral registration, SSE/DONE, WebSocket, long stream and scoped withdrawal passed; already-running Caddy load balancer; only synthetic DNS used")
				return
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	t.Fatal("Mac runner did not finish before fixture timeout")
}
