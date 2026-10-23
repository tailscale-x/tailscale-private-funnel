package tsconnection

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	tsapi "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net"
	"runtime/pprof"
	"strconv"
	"tailscale.com/ipn"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

func TestLiveFreshServiceOnRunningClient(t *testing.T) {
	if os.Getenv("FUNNEL_LIVE_FRESH_SERVICE") != "1" {
		t.Skip("requires protected issuer and Service credentials")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	home, _ := os.UserHomeDir()
	credentials := func(purpose string) tsapi.Client {
		read := func(name string) string {
			b, e := os.ReadFile(filepath.Join(home, ".config/tailscale-private-funnel/credentials/main-"+purpose, name))
			if e != nil {
				t.Fatal(e)
			}
			return strings.TrimSpace(string(b))
		}
		return tsapi.Client{ClientID: read("client-id"), ClientSecret: read("client-secret"), Tailnet: "-"}
	}
	issuer, services := credentials("issuer"), credentials("services")
	port := 8080
	if raw := os.Getenv("FUNNEL_LIVE_SERVICE_PORT"); raw != "" {
		var err error
		port, err = strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			t.Fatal("invalid test port")
		}
	}
	memory := os.Getenv("FUNNEL_LIVE_MEMORY")
	start := func(role identity.Role) *tsnet.Server {
		k, e := issuer.CreateWorkloadAuthKey(ctx, string(role), "fresh-service-qa", []string{identity.Tag("main", role)}, true)
		if e != nil {
			t.Fatal(e)
		}
		s := &tsnet.Server{Dir: t.TempDir(), Hostname: fmt.Sprintf("route-qa-%s-%d", role, time.Now().UnixNano()), AuthKey: k, Ephemeral: true, AdvertiseTags: []string{identity.Tag("main", role)}, Logf: func(f string, args ...any) { t.Logf(string(role)+": "+f, args...) }, UserLogf: func(string, ...any) {}}
		if memory == "both" || memory == "host" && role == identity.TailnetGateway {
			s.Store = new(mem.Store)
		}
		if _, e = s.Up(ctx); e != nil {
			t.Fatal(e)
		}
		s.AuthKey = ""
		t.Cleanup(func() {
			lc, _ := s.LocalClient()
			c, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			_ = lc.Logout(c)
			_ = s.Close()
		})
		return s
	}
	lb := Wrap(start(identity.LoadBalancer))
	if os.Getenv("FUNNEL_LIVE_ACCEPT_ROUTES") == "1" {
		api, _ := lb.LocalClient()
		if _, err := api.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{RouteAll: true}, RouteAllSet: true}); err != nil {
			t.Fatal(err)
		}
	}
	service := fmt.Sprintf("svc:tailscale-private-funnel-route-qa-%d", time.Now().Unix())
	meta, e := services.CreateService(ctx, tsapi.Service{Name: service, Ports: []string{fmt.Sprintf("tcp:%d", port)}, Tags: []string{identity.ServiceTag("main", false)}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		c, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_ = services.DeleteService(c, service)
	}()
	host := Wrap(start(identity.TailnetGateway))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "fresh-service") })
	if os.Getenv("FUNNEL_LIVE_CONFIG_FIRST") == "1" || os.Getenv("FUNNEL_LIVE_BARRIER") == "1" {
		backend := httptest.NewServer(handler)
		defer backend.Close()
		api, _ := host.LocalClient()
		forward := &Host{Client: api, API: api, DataDir: t.TempDir()}

		if os.Getenv("FUNNEL_LIVE_BARRIER") == "1" {
			cfg := new(ipn.ServeConfig)
			cfg.SetTCPForwardingForService(uint16(port), backend.Listener.Addr().String(), false, tailcfg.ServiceName(service), 0, "")
			if err := api.SetServeConfig(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			authorised := false
			for i := 0; i < 40; i++ {
				st, err := api.Status(ctx)
				if err != nil {
					t.Fatal(err)
				}
				maps, err := tailcfg.UnmarshalNodeCapJSON[tailcfg.ServiceIPMappings](st.Self.CapMap, tailcfg.NodeAttrServiceHost)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range maps {
					authorised = authorised || len(m[tailcfg.ServiceName(service)]) > 0
				}
				if authorised {
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
			if !authorised {
				t.Fatal("Service approval absent before advertisement")
			}
			t.Log("Service configuration acknowledged before advertisement")
			if _, err := api.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{AdvertiseServices: []string{service}}, AdvertiseServicesSet: true}); err != nil {
				t.Fatal(err)
			}
		} else {
			if e := forward.ForwardService(ctx, service, uint16(port), backend.Listener.Addr().String()); e != nil {
				t.Fatal(e)
			}
			defer forward.Close()
		}
	} else {
		ln, e := host.ListenService(service, tsnet.ServiceModeTCP{Port: uint16(port)})
		if e != nil {
			t.Fatal(e)
		}
		server := &http.Server{Handler: handler}
		defer server.Close()
		go server.Serve(ln)
	}

	if os.Getenv("FUNNEL_LIVE_RESTART_PUBLISHER") == "1" {
		api, _ := host.LocalClient()
		before, _ := api.Status(ctx)
		if err := api.Start(ctx, ipn.Options{}); err != nil {
			t.Fatal(err)
		}
		after, err := host.Up(ctx)
		if err != nil || after.Self.ID != before.Self.ID {
			t.Fatal("publisher identity changed", err)
		}
	}

	if os.Getenv("FUNNEL_LIVE_REFRESH_SERVICE") == "1" {
		for i := 0; i < 20; i++ {
			hosts, err := services.ListServiceHosts(ctx, service)
			if err != nil {
				t.Fatal(err)
			}
			ready := false
			for _, h := range hosts {
				ready = ready || h.Configured == "ready"
			}
			if ready {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		meta.DisplayName = "Ready forwarding"
		if os.Getenv("FUNNEL_LIVE_RETAG_SERVICE") == "1" {
			meta.Tags = append(meta.Tags, identity.Tag("main", identity.Control))
		}
		if _, err := services.CreateService(ctx, meta); err != nil {
			t.Fatal(err)
		}
	}
	client := lb.HTTPClient()
	client.Timeout = 3 * time.Second
	began := time.Now()
	captured := false
	for time.Since(began) < 45*time.Second {
		r, e := client.Get("http://" + net.JoinHostPort(meta.Addresses[0], strconv.Itoa(port)) + "/")
		if e == nil {
			b, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if string(b) != "fresh-service" {
				t.Fatal(string(b))
			}
			t.Log("fresh Service reachable", time.Since(began))
			return
		}
		t.Log("pending", time.Since(began), e)
		if !captured && time.Since(began) > 10*time.Second {
			captured = true
			f, err := os.OpenFile("/tmp/funnel-service-pending-stacks.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err == nil {
				_ = pprof.Lookup("goroutine").WriteTo(f, 2)
				f.Close()
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatal("existing client did not receive a fresh Service route within 45 seconds")
}
