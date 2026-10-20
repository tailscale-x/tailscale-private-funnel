package web

import (
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"testing"
	"time"
)

func TestConnectivityLossPreservesWorkingRoutesAndDNS(t *testing.T) {
	for _, failure := range []string{"missing reports", "fresh empty reports", "backend unreachable", "report sweeper", "node sweeper"} {
		t.Run(failure, func(t *testing.T) {
			c, s, ctx := retentionFixture(t)
			first, err := c.nodeConfiguration(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var before nodeconfig.Configuration
			if err = json.Unmarshal(first.Payload, &before); err != nil || len(before.Routes) != 1 {
				t.Fatal(before, err)
			}
			if failure == "missing reports" {
				loseReports(t, s)
			} else if failure == "report sweeper" {
				_, err = c.ExpireReports(t.Context(), -time.Hour)
			} else if failure == "node sweeper" {
				_, err = c.CleanupStaleNodes(t.Context(), -time.Hour)
			} else {
				snapshots, e := s.Reports()
				if e != nil {
					t.Fatal(e)
				}
				for id := range snapshots {
					xs := []dns.GatewayExposure{}
					if failure == "backend unreachable" {
						xs = []dns.GatewayExposure{{ApplicationID: "app0", ServiceName: "svc:app0", Port: 8080, PublicHostname: "app0.example.com", Reachable: false}}
					}
					env, e := report.NewSnapshot("report", id, map[string]any{"expires_at": time.Now().Add(time.Minute).Unix(), "exposures": xs}, time.Now())
					if e != nil {
						t.Fatal(e)
					}
					b, _ := json.Marshal(env)
					if e = s.AcceptReport(id, env.Nonce, b); e != nil {
						t.Fatal(e)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				// Each new Control models process replacement without a memory cache.
				current := &Control{Store: s, Installation: c.Installation, ReportTTL: c.ReportTTL}
				got, e := current.nodeConfiguration(ctx)
				if e != nil {
					t.Fatal(e)
				}
				var cfg nodeconfig.Configuration
				if e = json.Unmarshal(got.Payload, &cfg); e != nil || len(cfg.Routes) != 1 || !sameRoute(before.Routes[0], cfg.Routes[0]) {
					t.Fatalf("lost route: %+v %v", cfg, e)
				}
				if e = current.reconcileExposures(t.Context()); e != nil {
					t.Fatal(e)
				}
				records, e := exposureLedger(t, s, "fixture", "example.com", "app0.example.com")
				if e != nil || len(records) != 1 {
					t.Fatal("lost owned DNS", records, e)
				}
			}
		})
	}
}

func TestRetainedRoutesStillApplyCurrentAuthority(t *testing.T) {
	for _, action := range []string{"policy denial", "gateway revoked", "application disabled", "hostname removed", "backend changed", "rules changed"} {
		t.Run(action, func(t *testing.T) {
			c, s, ctx := retentionFixture(t)
			if _, e := c.nodeConfiguration(ctx); e != nil {
				t.Fatal(e)
			}
			loseReports(t, s)
			apps, e := s.Applications()
			if e != nil {
				t.Fatal(e)
			}
			a := apps[0]
			switch action {
			case "policy denial":
				e = s.SetJSON("exposure_policy", policy.Document{Revision: 2, Rules: []policy.Rule{}})
			case "gateway revoked":
				e = s.RevokeNode("gateway")
			case "application disabled":
				a.Enabled = false
				e = s.UpsertApplication(a)
			case "hostname removed":
				a.PublicHostnames = nil
				e = s.UpsertApplication(a)
			case "backend changed":
				a.ServiceFQDN = "different.example.ts.net"
				e = s.UpsertApplication(a)
			case "rules changed":
				a.HTTPRules = append(a.HTTPRules, proxy.HTTPRule{Matches: []proxy.Match{{Path: "/changed"}}})
				e = s.UpsertApplication(a)
			}
			if e != nil {
				t.Fatal(e)
			}
			got, e := c.nodeConfiguration(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var cfg nodeconfig.Configuration
			if e = json.Unmarshal(got.Payload, &cfg); e != nil || len(cfg.Routes) != 0 {
				t.Fatal("explicit change retained old route", cfg, e)
			}
			// The empty authoritative configuration must replace the retained one.
			got, e = c.nodeConfiguration(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(got.Payload, &cfg); e != nil || len(cfg.Routes) != 0 {
				t.Fatal("route resurrected", cfg, e)
			}
		})
	}
}

func TestPodReplacementKeepsPreviouslyReadyRoute(t *testing.T) {
	for _, replacement := range []string{"new pod", "no ready pods", "different node"} {
		t.Run(replacement, func(t *testing.T) {
			c, s, ctx := retentionFixture(t)
			apps, err := s.Applications()
			if err != nil {
				t.Fatal(err)
			}
			a := apps[0]
			backend := proxy.Backend{Key: "service-uid:8080", ServiceName: a.ServiceName, Service: a.ServiceFQDN, Port: a.ServicePort, Address: "10.96.0.1:8080", Weight: 1, Endpoints: []proxy.PodEndpoint{{Address: "10.244.1.10:8080", PodUID: "old-pod", NodeName: "node1", NodeUID: "node-uid1"}}}
			a.HTTPRules = []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/"}}, Backends: []proxy.Backend{backend}}}
			if err = s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			env, err := c.nodeConfiguration(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var before nodeconfig.Configuration
			if err = json.Unmarshal(env.Payload, &before); err != nil || len(before.Routes) != 1 {
				t.Fatalf("initial route: %+v %v", before, err)
			}
			loseReports(t, s)
			endpoint := proxy.PodEndpoint{Address: "10.244.1.11:8080", PodUID: "new-pod", NodeName: "node1", NodeUID: "node-uid1"}
			if replacement == "different node" {
				endpoint.Address = "10.244.2.11:8080"
				endpoint.NodeName = "node2"
				endpoint.NodeUID = "node-uid2"
			}
			a.HTTPRules[0].Backends[0].Endpoints = []proxy.PodEndpoint{endpoint}
			if replacement == "no ready pods" {
				a.HTTPRules[0].Backends[0].Endpoints = nil
			}
			if err = s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			env, err = c.nodeConfiguration(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var after nodeconfig.Configuration
			if err = json.Unmarshal(env.Payload, &after); err != nil || len(after.Routes) != 1 {
				t.Fatalf("Pod discovery withdrew stable public route: %+v %v", after, err)
			}
			if !sameRoute(before.Routes[0], after.Routes[0]) {
				t.Fatal("replacement changed public forwarding definition")
			}
		})
	}
}

func TestRouteEquivalenceRetainsBackendAuthority(t *testing.T) {
	base := proxy.Route{Hostname: "app.example.com", Service: "100.64.0.10", Port: 8080, Rules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/api"}}, Backends: []proxy.Backend{{Key: "service-uid:8080", ServiceName: "svc:app", Service: "100.64.0.10", Port: 8080, Weight: 1, Endpoints: []proxy.PodEndpoint{{Address: "10.244.1.10:8080", PodUID: "old-pod"}}}}}}}
	for _, change := range []string{"backend UID", "backend Service", "backend port", "weight", "path", "rewrite", "headers"} {
		t.Run(change, func(t *testing.T) {
			wire, _ := json.Marshal(base)
			var candidate proxy.Route
			_ = json.Unmarshal(wire, &candidate)
			b := &candidate.Rules[0].Backends[0]
			switch change {
			case "backend UID":
				b.Key = "another-service-uid:8080"
			case "backend Service":
				b.Service = "100.64.0.11"
			case "backend port":
				b.Port = 8081
			case "weight":
				b.Weight = 2
			case "path":
				candidate.Rules[0].Matches[0].Path = "/other"
			case "rewrite":
				candidate.Rules[0].Rewrite = &proxy.PrefixRewrite{ReplacePrefix: "/internal"}
			case "headers":
				candidate.Rules[0].RequestHeaders.Set = map[string]string{"X-Example": "changed"}
			}
			if sameRoute(base, candidate) {
				t.Fatalf("%s change incorrectly retained", change)
			}
		})
	}
}
