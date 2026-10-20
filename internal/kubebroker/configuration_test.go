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

func TestColdConfigurationHasStartupBudgetButWarmOutageRemainsBounded(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	env, _ := report.NewSnapshot("configuration", "controller", nodeconfig.Configuration{NodeID: "controller", Revision: "one"}, now)
	calls := 0
	cache := &ConfigurationCache{NodeID: "controller", Fetch: func(ctx context.Context) (report.Snapshot, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("unbounded fetch")
		}
		remaining := time.Until(deadline)
		if calls == 1 {
			if remaining < 15*time.Second || remaining > 21*time.Second {
				t.Fatal("cold bootstrap budget", remaining)
			}
			return env, nil
		}
		if remaining > 3*time.Second {
			t.Fatal("warm fallback blocked for cold budget", remaining)
		}
		return report.Snapshot{}, context.DeadlineExceeded
	}}
	if _, err := cache.Get(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := cache.Get(t.Context())
	if err != nil || got.Timestamp != env.Timestamp {
		t.Fatal("warm outage lost or refreshed cached authorization", err)
	}
}

func TestConfigurationExpiryRecheckedAfterSlowFetch(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "cached"}[fallback], func(t *testing.T) {
			now := time.Now().Truncate(time.Second)
			env, _ := report.NewSnapshot("configuration", "controller", nodeconfig.Configuration{NodeID: "controller", Revision: "one"}, now)
			calls := 0
			cache := &ConfigurationCache{NodeID: "controller", Now: func() time.Time { return now }, Fetch: func(context.Context) (report.Snapshot, error) {
				calls++
				if calls > 1 {
					now = now.Add(10 * time.Second)
					if fallback {
						return report.Snapshot{}, errors.New("control offline")
					}
				}
				return env, nil
			}}
			if _, err := cache.Get(t.Context()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(5*time.Minute - time.Second)
			if _, err := cache.Get(t.Context()); err == nil {
				t.Fatal("expired configuration returned after slow fetch")
			}
		})
	}
}
