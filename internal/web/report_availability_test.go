package web

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func TestForwarderHeartbeatDoesNotWaitForProviderReconciliation(t *testing.T) {
	c, s := testControl(t)
	if e := s.UpsertNode("parent", "k8s-gateway", "parent"); e != nil {
		t.Fatal(e)
	}
	pub, key, _ := report.GenerateKey()
	public := base64.RawStdEncoding.EncodeToString(pub)
	child, e := s.ReserveForwarder("parent", "kube-node", "node", public)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.BindNodeIdentity(child.ID, public, "tail-child", nil); e != nil {
		t.Fatal(e)
	}
	env, _ := report.NewEnvelope(child.ID, map[string]any{"exposures": []any{}, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, key, time.Now())
	ctx := WithPrincipal(httptest.NewRequest("POST", "/", nil), Principal{NodeID: "tail-child", Tagged: true}).Context()
	c.cleanupMu.Lock()
	done := make(chan error, 1)
	go func() { done <- c.acceptExposureReport(ctx, env) }()
	select {
	case e = <-done:
		c.cleanupMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		c.cleanupMu.Unlock()
		<-done
		t.Fatal("external reconciliation blocked forwarder heartbeat")
	}
	if e = s.RevokeNode("parent"); e != nil {
		t.Fatal(e)
	}
	env, _ = report.NewEnvelope(child.ID, map[string]any{"exposures": []any{}, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, key, time.Now())
	if e = c.acceptExposureReport(ctx, env); e == nil {
		t.Fatal("revoked parent bypassed fast path")
	}
}
