package kubebroker

import (
	"connectrpc.com/connect"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func TestConfigurationSurvivesOnlyBoundedOutage(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	pub, key, _ := report.GenerateKey()
	env, _ := report.NewEnvelopeWithPurpose("configuration", "controller", nodeconfig.Configuration{NodeID: "controller", Revision: "one"}, pub, key, now)
	fail := false
	var failure error = errors.New("control unavailable")
	cache := &ConfigurationCache{PublicKey: base64.RawStdEncoding.EncodeToString(pub), NodeID: "controller", Now: func() time.Time { return now }, Fetch: func(context.Context) (report.Envelope, error) {
		if fail {
			return report.Envelope{}, failure
		}
		return env, nil
	}}
	if _, e := cache.Get(t.Context()); e != nil {
		t.Fatal(e)
	}
	fail = true
	now = now.Add(2 * time.Minute)
	cached, e := cache.Get(t.Context())
	if e != nil || cached.Signature != env.Signature || cached.Timestamp != env.Timestamp {
		t.Fatal("configuration interrupted or expiry extended", e)
	}
	now = now.Add(4 * time.Minute)
	if _, e = cache.Get(t.Context()); e == nil {
		t.Fatal("expired configuration authorized")
	}
	fail = false
	env, _ = report.NewEnvelopeWithPurpose("configuration", "controller", nodeconfig.Configuration{NodeID: "controller", Revision: "two"}, pub, key, now)
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
	env.Signature = "invalid"
	if _, e = cache.Get(t.Context()); e == nil {
		t.Fatal("invalid live signature masked by cache")
	}
}
