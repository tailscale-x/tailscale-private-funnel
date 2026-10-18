package web

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShutdownRequiresReceiptConfirmationBeforeDeviceDeletion(t *testing.T) {
	c, s := testControl(t)
	s.SetJSON("tailnet", map[string]string{"name": "test"})
	s.SetSecret("tailscale_oauth_client_id", "id")
	s.SetSecret("tailscale_oauth_client_secret", "secret")
	s.UpsertNode("self", "tailnet-gateway", "self")
	s.BindNodeIdentity("self", "public-key", "tailnet-self", nil)
	deleted := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth/token"):
			writeJSON(w, map[string]string{"access_token": "fixture"})
		case r.Method == "DELETE":
			deleted++
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/devices"):
			writeJSON(w, map[string]any{"devices": []map[string]string{{"id": "device-1", "nodeId": "tailnet-self"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.Handler().ServeHTTP(w, WithPrincipal(r, Principal{NodeID: "tailnet-self", Tagged: true, Hostname: "self"}))
	}))
	defer server.Close()
	client := funnelv1connect.NewNodeServiceClient(server.Client(), server.URL)
	ctx := context.Background()
	r, e := client.Shutdown(ctx, connect.NewRequest(&v1.ShutdownRequest{}))
	if e != nil || !r.Msg.Ok {
		t.Fatal(r, e)
	}
	c.RetryNodeCleanup(ctx)
	if deleted != 0 {
		t.Fatal("cut off caller before acknowledgment")
	}
	if _, e = client.GetConfig(ctx, connect.NewRequest(&v1.Empty{})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("revoked identity retained runtime access", e)
	}
	if _, e = client.Shutdown(ctx, connect.NewRequest(&v1.ShutdownRequest{Confirm: true})); e != nil {
		t.Fatal(e)
	}
	c.RetryNodeCleanup(ctx)
	if deleted != 0 {
		t.Fatal("deleted before acknowledgment reply grace")
	}
	var state nodeCleanupState
	s.GetJSON("node_cleanup:self", &state)
	state.DeleteAfter = time.Now().Add(-time.Second).Unix()
	s.SetJSON("node_cleanup:self", state)
	c.RetryNodeCleanup(ctx)
	if deleted != 1 {
		t.Fatal("device deletion not completed", deleted)
	}
	s.GetJSON("node_cleanup:self", &state)
	if state.Pending {
		t.Fatal("cleanup still pending")
	}
}
