package gateway

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type nodeFixture struct {
	funnelv1connect.UnimplementedNodeServiceHandler
	envelope *v1.SignedEnvelope
	health   int
}

func (f *nodeFixture) ResolveApplication(context.Context, *connect.Request[v1.ResolveApplicationRequest]) (*connect.Response[v1.SignedEnvelope], error) {
	return connect.NewResponse(f.envelope), nil
}
func (f *nodeFixture) ReportHealth(context.Context, *connect.Request[v1.NodeHealth]) (*connect.Response[v1.Empty], error) {
	f.health++
	return connect.NewResponse(&v1.Empty{}), nil
}
func resolverFixture(t *testing.T) (RPCResolver, *nodeFixture) {
	t.Helper()
	pub, key, e := report.GenerateKey()
	if e != nil {
		t.Fatal(e)
	}
	config := nodeconfig.Configuration{NodeID: "node-1", Applications: []store.ApplicationRecord{{ID: "checkout", ServiceName: "svc:checkout", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"checkout.dev.example.com"}, Enabled: true}}, IssuedAt: time.Now().Unix()}
	config.Revision = nodeconfig.Revision(config)
	envelope, e := report.NewEnvelopeWithPurpose("configuration", "node-1", config, pub, key, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	wire, e := report.Wire(envelope)
	if e != nil {
		t.Fatal(e)
	}
	fixture := &nodeFixture{envelope: wire}
	path, h := funnelv1connect.NewNodeServiceHandler(fixture)
	mux := http.NewServeMux()
	mux.Handle(path, h)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return RPCResolver{BaseURL: server.URL, NodeID: "node-1", ControlKey: base64.RawStdEncoding.EncodeToString(pub), Client: server.Client()}, fixture
}
func TestRPCResolverMatchesApprovedPublicHostname(t *testing.T) {
	r, _ := resolverFixture(t)
	a, e := r.Resolve(t.Context(), Spec{Hostname: "checkout.dev.example.com.", Port: 8080})
	if e != nil || a.ApplicationID != "checkout" {
		t.Fatalf("assignment=%v error=%v", a, e)
	}
}
func TestRPCResolverRejectsUnapprovedPort(t *testing.T) {
	r, _ := resolverFixture(t)
	if _, e := r.Resolve(t.Context(), Spec{Hostname: "checkout.dev.example.com", Port: 8443}); e == nil {
		t.Fatal("unapproved port accepted")
	}
}
func TestRPCResolverRejectsWrongSignedPurpose(t *testing.T) {
	r, f := resolverFixture(t)
	f.envelope.Purpose = "report"
	if _, e := r.Resolve(t.Context(), Spec{Hostname: "checkout.dev.example.com", Port: 8080}); e == nil {
		t.Fatal("report accepted as configuration")
	}
}
func TestRPCResolverMarksControlOutage(t *testing.T) {
	_, e := (RPCResolver{}).Resolve(t.Context(), Spec{Hostname: "checkout.dev.example.com", Port: 8080})
	if !errors.Is(e, ErrControlUnavailable) {
		t.Fatalf("outage=%v", e)
	}
}
func TestRPCResolverReportsHealth(t *testing.T) {
	r, f := resolverFixture(t)
	if e := r.ReportHealth(t.Context(), map[string]any{"backends": 1}); e != nil {
		t.Fatal(e)
	}
	if f.health != 1 {
		t.Fatal("health not submitted")
	}
}

func TestRPCResolverSelectsSignedPathAssignment(t *testing.T) {
	r, f := resolverFixture(t)
	pub, key, _ := report.GenerateKey()
	r.ControlKey = base64.RawStdEncoding.EncodeToString(pub)
	config := nodeconfig.Configuration{NodeID: "node-1", IssuedAt: time.Now().Unix()}
	for _, path := range []string{"/api", "/admin"} {
		config.Applications = append(config.Applications, store.ApplicationRecord{ID: path, ServiceName: "svc:" + path[1:], ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com"}, Enabled: true, HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: path}}, Rewrite: &proxy.PrefixRewrite{ReplacePrefix: "/"}}}})
	}
	config.Revision = nodeconfig.Revision(config)
	env, err := report.NewEnvelopeWithPurpose("configuration", "node-1", config, pub, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	f.envelope, err = report.Wire(env)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := r.Resolve(t.Context(), Spec{Hostname: "app.example.com", Port: 8080, PathPrefix: "/admin", Rewrite: &proxy.PrefixRewrite{ReplacePrefix: "/"}})
	if err != nil || assignment.ApplicationID != "/admin" {
		t.Fatal(assignment, err)
	}
	if _, err = r.Resolve(t.Context(), Spec{Hostname: "app.example.com", Port: 8080, PathPrefix: "/admin", Rewrite: &proxy.PrefixRewrite{ReplacePrefix: "/other"}}); err == nil {
		t.Fatal("mismatched rewrite accepted")
	}
}
