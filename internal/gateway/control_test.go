package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func signedConfig(t *testing.T, nodeID string) ([]byte, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := nodeconfig.Configuration{NodeID: nodeID, Applications: []store.ApplicationRecord{{ID: "checkout", ServiceName: "svc:tailscale-private-funnel-app-checkout", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"checkout.dev.example.com"}, Enabled: true}}, IssuedAt: time.Now().Unix()}
	config.Revision = nodeconfig.Revision(config)
	e, err := report.NewEnvelope(nodeID, config, pub, priv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(e)
	return b, base64.RawStdEncoding.EncodeToString(pub)
}

func TestHTTPResolverMatchesApprovedPublicHostname(t *testing.T) {
	payload, key := signedConfig(t, "node-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node/config" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	assignment, err := (HTTPResolver{BaseURL: srv.URL, NodeID: "node-1", ControlKey: key, Client: srv.Client()}).Resolve(context.Background(), "checkout.dev.example.com.", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.ApplicationID != "checkout" || assignment.ServiceName != "svc:tailscale-private-funnel-app-checkout" {
		t.Fatalf("assignment=%#v", assignment)
	}
}

func TestHTTPResolverRejectsUnapprovedPort(t *testing.T) {
	payload, key := signedConfig(t, "node-1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	if _, err := (HTTPResolver{BaseURL: srv.URL, NodeID: "node-1", ControlKey: key, Client: srv.Client()}).Resolve(context.Background(), "checkout.dev.example.com", 8443); err == nil {
		t.Fatal("accepted a backend port that was not approved")
	}
}

func TestHTTPResolverMarksControlOutage(t *testing.T) {
	_, err := (HTTPResolver{}).Resolve(context.Background(), "checkout.dev.example.com", 8080)
	if !errors.Is(err, ErrControlUnavailable) {
		t.Fatalf("error=%v, want ErrControlUnavailable", err)
	}
}

func TestHTTPResolverReportsHealthToEnrolledNode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/node/health" {
			t.Fatalf("request=%s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := (HTTPResolver{BaseURL: srv.URL, NodeID: "node-1", Client: srv.Client()}).ReportHealth(context.Background(), map[string]any{"backends": 1}); err != nil {
		t.Fatal(err)
	}
}
