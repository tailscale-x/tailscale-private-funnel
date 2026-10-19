package kubebroker

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func TestConfigurationSurvivesOnlyBoundedOutage(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	env, _ := report.NewSnapshot("configuration", "controller", nodeconfig.Configuration{NodeID: "controller", Revision: "one"}, now)
	fail := false
	var failure error = errors.New("control unavailable")
	cache := &ConfigurationCache{NodeID: "controller", Now: func() time.Time { return now }, Fetch: func(context.Context) (report.Snapshot, error) {
		if fail {
			return report.Snapshot{}, failure
		}
		return env, nil
	}}
	if _, e := cache.Get(t.Context()); e != nil {
		t.Fatal(e)
	}
	fail = true
	now = now.Add(2 * time.Minute)
	cached, e := cache.Get(t.Context())
	if e != nil || cached.Timestamp != env.Timestamp {
		t.Fatal("configuration interrupted or expiry extended", e)
	}
	now = now.Add(4 * time.Minute)
	if _, e = cache.Get(t.Context()); e == nil {
		t.Fatal("expired configuration authorized")
	}
	fail = false
	env, _ = report.NewSnapshot("configuration", "controller", nodeconfig.Configuration{NodeID: "controller", Revision: "two"}, now)
	if _, e = cache.Get(t.Context()); e != nil {
		t.Fatal("fresh configuration did not recover", e)
	}
	fail = true
	failure = connect.NewError(connect.CodePermissionDenied, errors.New("revoked"))
	if _, e = cache.Get(t.Context()); e == nil {
		t.Fatal("explicit denial hidden by cache")
	}
	failure = errors.New("offline")
	if _, e = cache.Get(t.Context()); e == nil {
		t.Fatal("denied configuration restored from old cache")
	}
	fail = false
	env.NodeID = "foreign"
	if _, e = cache.Get(t.Context()); e == nil {
		t.Fatal("foreign live configuration masked by cache")
	}
}
