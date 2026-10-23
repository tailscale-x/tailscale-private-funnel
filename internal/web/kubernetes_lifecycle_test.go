package web

import (
	"connectrpc.com/connect"
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

// A solver Service UID can sort before Harbor and change the legacy primary.
// Neither that bookkeeping change nor loss of reports may withdraw the catch-all.
func TestTemporaryKubernetesRoutePreservesCatchAll(t *testing.T) {
	c, s, ctx := retentionFixture(t)
	apps, err := s.Applications()
	if err != nil {
		t.Fatal(err)
	}
	a := apps[0]
	harbor := proxy.Backend{Key: "z-harbor:8080", ServiceName: a.ServiceName, Service: a.ServiceFQDN, Port: a.ServicePort, Weight: 1}
	catchAll := proxy.HTTPRule{Matches: []proxy.Match{{Path: "/"}}, Backends: []proxy.Backend{harbor}}
	a.HTTPRules = []proxy.HTTPRule{catchAll}
	a.HTTPBackends = []proxy.Backend{harbor}
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	get := func() nodeconfig.Configuration {
		t.Helper()
		env, e := c.nodeConfiguration(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var cfg nodeconfig.Configuration
		if e = json.Unmarshal(env.Payload, &cfg); e != nil {
			t.Fatal(e)
		}
		return cfg
	}
	if cfg := get(); len(cfg.Routes) != 1 {
		t.Fatal("initial Harbor route missing", cfg)
	}
	loseReports(t, s)
	solver := proxy.Backend{Key: "a-solver:8089", ServiceName: "svc:solver", Service: "100.64.0.99", Port: 8089, Weight: 1}
	challenge := proxy.HTTPRule{Matches: []proxy.Match{{Path: "/.well-known/acme-challenge/token", Exact: true}}, Backends: []proxy.Backend{solver}}
	a.HTTPRules = []proxy.HTTPRule{challenge, catchAll}
	a.HTTPBackends = []proxy.Backend{solver, harbor}
	a.ServiceName = solver.ServiceName
	a.ServiceFQDN = solver.Service
	a.ServicePort = solver.Port
	a.BackendPort = solver.Port
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	cfg := get()
	if len(cfg.Routes) != 1 || len(cfg.Routes[0].Rules) != 1 || publicRouteRevision(cfg.Routes[0].Rules) != publicRouteRevision([]proxy.HTTPRule{catchAll}) {
		t.Fatalf("pending solver withdrew Harbor or published unready solver: %+v", cfg.Routes)
	}
	// Deletion is authoritative: the solver must disappear, without an empty host.
	a.HTTPRules = []proxy.HTTPRule{catchAll}
	a.HTTPBackends = []proxy.Backend{harbor}
	a.ServiceName = harbor.ServiceName
	a.ServiceFQDN = harbor.Service
	a.ServicePort = harbor.Port
	a.BackendPort = harbor.Port
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	cfg = get()
	if len(cfg.Routes) != 1 || len(cfg.Routes[0].Rules) != 1 {
		t.Fatalf("solver deletion withdrew Harbor: %+v", cfg.Routes)
	}
}

func TestKubernetesRuleLifecycleReadinessAndAcknowledgment(t *testing.T) {
	c, s, ctx := retentionFixture(t)
	apps, err := s.Applications()
	if err != nil {
		t.Fatal(err)
	}
	a := apps[0]
	harbor := proxy.Backend{Key: "harbor:8080", ServiceName: a.ServiceName, Service: a.ServiceFQDN, Port: a.ServicePort, Weight: 1}
	solver := proxy.Backend{Key: "solver:8089", ServiceName: "svc:solver", Service: "100.64.0.99", Port: 8089, Weight: 1}
	catchAll := proxy.HTTPRule{Matches: []proxy.Match{{Path: "/"}}, Backends: []proxy.Backend{harbor}}
	challenge := proxy.HTTPRule{Matches: []proxy.Match{{Path: "/.well-known/acme-challenge/token", Exact: true}}, Backends: []proxy.Backend{solver}}
	a.HTTPRules = []proxy.HTTPRule{catchAll}
	a.HTTPBackends = []proxy.Backend{harbor}
	save := func() {
		t.Helper()
		if e := s.UpsertApplication(a); e != nil {
			t.Fatal(e)
		}
	}
	save()
	get := func() nodeconfig.Configuration {
		t.Helper()
		env, e := c.nodeConfiguration(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var cfg nodeconfig.Configuration
		if e = json.Unmarshal(env.Payload, &cfg); e != nil {
			t.Fatal(e)
		}
		return cfg
	}
	ack := func(cfg nodeconfig.Configuration) {
		t.Helper()
		_, e := (&nodeRPC{c}).Acknowledge(ctx, connect.NewRequest(&v1.ConfigAck{Revision: cfg.Revision}))
		if e != nil {
			t.Fatal(e)
		}
	}
	cfg := get()
	if len(cfg.Routes) != 1 {
		t.Fatal("initial route missing")
	}
	ack(cfg)
	a.HTTPRules = []proxy.HTTPRule{challenge, catchAll}
	a.HTTPBackends = []proxy.Backend{harbor, solver}
	save()
	snapshot := nodeconfig.KubernetesSnapshot{Hosts: []nodeconfig.KubernetesHost{{Hostname: a.PublicHostnames[0], Rules: a.HTTPRules, ResourceIDs: []string{"solver:1", "harbor:1"}}}}
	publication := func() map[string]string {
		return c.publishedKubernetesResources("gateway", snapshot, []store.ApplicationRecord{a})
	}
	expected := nodeconfig.KubernetesResourceRevisions(snapshot.Hosts)
	if p := publication(); p["harbor:1"] != expected["harbor:1"] || p["solver:1"] != "" {
		t.Fatal("hostname acknowledgment published solver", p)
	}
	cfg = get()
	if len(cfg.Routes) != 1 || len(cfg.Routes[0].Rules) != 1 {
		t.Fatal("pending solver disrupted Harbor", cfg.Routes)
	}
	// Merely sending the new snapshot does not invalidate the prior applied route.
	if p := publication(); p["harbor:1"] == "" {
		t.Fatal("configuration polling cleared applied acknowledgment")
	}
	send := func(reachable bool, service string) {
		t.Helper()
		env, e := report.NewSnapshot("report", "edge-id", map[string]any{"expires_at": time.Now().Add(time.Minute).Unix(), "exposures": []dns.GatewayExposure{{ApplicationID: a.ID, PublicHostname: a.PublicHostnames[0], ServiceName: service, Port: 8089, Reachable: reachable}}}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		e = c.acceptExposureReport(ctx, env)
		if service == "svc:forged" {
			if connect.CodeOf(e) != connect.CodePermissionDenied {
				t.Fatalf("forged backend report accepted: %v", e)
			}
			return
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	send(true, "svc:forged")
	send(false, solver.ServiceName)
	cfg = get()
	if len(cfg.Routes[0].Rules) != 1 {
		t.Fatal("unreachable solver published")
	}
	send(true, solver.ServiceName)
	cfg = get()
	if len(cfg.Routes) != 1 || len(cfg.Routes[0].Rules) != 2 {
		t.Fatal("reachable solver not installed", cfg.Routes)
	}
	if p := publication(); p["solver:1"] != "" {
		t.Fatal("sent revision treated as applied")
	}
	ack(cfg)
	if p := publication(); p["solver:1"] != expected["solver:1"] || p["harbor:1"] != expected["harbor:1"] {
		t.Fatal("applied rules not published", p)
	}
	// Pod endpoint loss/replacement is discovery, not a public rule mutation.
	loseReports(t, s)
	a.HTTPRules[0].Backends[0].Endpoints = []proxy.PodEndpoint{{PodUID: "replacement", Address: "10.244.2.10:8089", NodeUID: "node2"}}
	save()
	cfg = get()
	if len(cfg.Routes) != 1 || len(cfg.Routes[0].Rules) != 2 {
		t.Fatal("Pod replacement withdrew rules")
	}
	// Route or namespace deletion removes only the solver; a stale edge ack must
	// not be attributed to the newly sent configuration.
	old := cfg
	a.HTTPRules = []proxy.HTTPRule{catchAll}
	a.HTTPBackends = []proxy.Backend{harbor}
	save()
	cfg = get()
	if len(cfg.Routes) != 1 || len(cfg.Routes[0].Rules) != 1 {
		t.Fatal("deletion withdrew Harbor")
	}
	if _, e := (&nodeRPC{c}).Acknowledge(ctx, connect.NewRequest(&v1.ConfigAck{Revision: old.Revision})); connect.CodeOf(e) != connect.CodeAborted {
		t.Fatalf("stale ack accepted: %v", e)
	}
	ack(cfg)
	// Explicit policy denial still withdraws retained traffic.
	if e := s.SetJSON("exposure_policy", policy.Document{Revision: 2}); e != nil {
		t.Fatal(e)
	}
	if cfg = get(); len(cfg.Routes) != 0 {
		t.Fatal("policy denial retained traffic", cfg.Routes)
	}
}

func TestKubernetesWeightedRuleWaitsForEveryBackend(t *testing.T) {
	c, s, ctx := retentionFixture(t)
	apps, err := s.Applications()
	if err != nil {
		t.Fatal(err)
	}
	a := apps[0]
	first := proxy.Backend{Key: "first", ServiceName: a.ServiceName, Service: a.ServiceFQDN, Port: a.ServicePort, Weight: 1}
	second := proxy.Backend{Key: "second", ServiceName: "svc:second", Service: "100.64.0.99", Port: 8089, Weight: 1}
	a.HTTPRules = []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/"}}, Backends: []proxy.Backend{first, second}}}
	a.HTTPBackends = []proxy.Backend{first, second}
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	// The fixture has a reachable report only for the first Service. Do not
	// publish a weighted rule whose second backend has never passed readiness.
	env, err := c.nodeConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cfg nodeconfig.Configuration
	if err = json.Unmarshal(env.Payload, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 0 {
		t.Fatal("weighted rule published before all backends were ready", cfg.Routes)
	}
}
