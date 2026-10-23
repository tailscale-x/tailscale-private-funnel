package web

import (
	"context"
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
)

func TestTunnelPhoneHomeAnyVerifiedPeerAndFixedRole(t *testing.T) {
	c, s, _ := localTunnelFixture(t)
	_ = s.SetSecret("tailscale_issuer_client_id", "test-issuer")
	_ = s.SetSecret("tailscale_issuer_client_secret", "test-secret")
	issued := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fake"})
			return
		}
		var body struct {
			Expiry       int `json:"expirySeconds"`
			Capabilities struct {
				Devices struct {
					Create struct {
						Tags                               []string `json:"tags"`
						Reusable, Ephemeral, Preauthorized bool
					} `json:"create"`
				} `json:"devices"`
			} `json:"capabilities"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		d := body.Capabilities.Devices.Create
		if body.Expiry != 900 || d.Reusable || !d.Ephemeral || !d.Preauthorized || len(d.Tags) != 1 || d.Tags[0] != identity.Tag("main", identity.TailnetGateway) {
			t.Errorf("wrong issuance: %+v", body)
		}
		issued++
		_ = json.NewEncoder(w).Encode(map[string]string{"key": "tskey-auth-disposable-test"})
	}))
	defer api.Close()
	c.TailscaleClient.APIBase = api.URL
	c.TailscaleClient.OAuthBase = api.URL
	c.TailscaleClient.HTTP = api.Client()
	request := &v1.TunnelBootstrapRequest{Installation: "main", Name: "mac-tunnel", Hostname: "app.example.com", Port: 8080}
	for _, p := range []Principal{{NodeID: "ordinary-user", Login: "member@example.com"}, {NodeID: "tagged-peer", Tagged: true, Tags: []string{identity.Tag("other", identity.DockerGateway)}}} {
		server := rpcServer(t, c, p)
		client := funnelv1connect.NewDeploymentServiceClient(server.Client(), server.URL)
		r, err := client.BootstrapTunnel(t.Context(), connect.NewRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		if r.Msg.AuthKey == "" || r.Msg.ExpiresAt <= time.Now().Unix() || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(r)
		}
		management := funnelv1connect.NewManagementServiceClient(server.Client(), server.URL)
		if _, err = management.GetSettings(t.Context(), connect.NewRequest(&v1.Empty{})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("bootstrap peer gained management access", err)
		}
	}
	server := rpcServer(t, c, Principal{})
	client := funnelv1connect.NewDeploymentServiceClient(server.Client(), server.URL)
	if _, err := client.BootstrapTunnel(t.Context(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal(err)
	}
	server = rpcServer(t, c, Principal{NodeID: "ordinary-user"})
	client = funnelv1connect.NewDeploymentServiceClient(server.Client(), server.URL)
	bad := proto.Clone(request).(*v1.TunnelBootstrapRequest)
	bad.Installation = "other"
	if _, err := client.BootstrapTunnel(t.Context(), connect.NewRequest(bad)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
	bad = proto.Clone(request).(*v1.TunnelBootstrapRequest)
	bad.Hostname = "unmanaged.invalid"
	if _, err := client.BootstrapTunnel(t.Context(), connect.NewRequest(bad)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := client.BootstrapTunnel(context.Background(), connect.NewRequest(request)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.BootstrapTunnel(t.Context(), connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("issuance not rate limited", err)
	}
	if issued != 6 {
		t.Fatal(issued)
	}
}
