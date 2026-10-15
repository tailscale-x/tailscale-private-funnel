package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func TestLoadBalancerConfigurationSignatureSurvivesHTTPEncoding(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNodeEnrollment("edge", "Edge", "loadbalancer", "edge")
	_ = s.BindNodeIdentity("edge", "unused-signing-key", "other-edge-tailnet", nil)
	req := WithPrincipal(httptest.NewRequest("GET", "/api/node/config", nil), Principal{NodeID: "other-edge-tailnet", Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("config: %d %s", w.Code, w.Body.String())
	}
	var env report.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	pub, _, err := s.ConfigurationKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeconfig.Verify(env, base64.RawStdEncoding.EncodeToString(pub), "edge", time.Now()); err != nil {
		t.Fatalf("HTTP config signature: %v body=%s", err, w.Body.String())
	}
}
