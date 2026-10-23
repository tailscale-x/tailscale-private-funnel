package web

import (
	"context"
	"errors"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReportSerializationIsComponentScopedAndCancelable(t *testing.T) {
	c, s := testControl(t)
	if e := s.UpsertNode("reporter", "docker-gateway", "reporter"); e != nil {
		t.Fatal(e)
	}
	if e := bindTestNode(s, "reporter", "tail-reporter", nil); e != nil {
		t.Fatal(e)
	}
	unlock, e := c.lockReport(t.Context(), "blocked")
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, e = c.lockReport(ctx, "blocked"); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("queued request ignored cancellation", e)
	}
	env, _ := report.NewSnapshot("report", "reporter", map[string]any{"exposures": []any{}, "expires_at": time.Now().Add(time.Minute).Unix()}, time.Now())
	ctx, cancel = context.WithTimeout(WithPrincipal(httptest.NewRequest("POST", "/", nil), Principal{NodeID: "tail-reporter", Tagged: true}).Context(), time.Second)
	defer cancel()
	if e = c.acceptExposureReport(ctx, env); e != nil {
		t.Fatal("another component blocked report", e)
	}
}
