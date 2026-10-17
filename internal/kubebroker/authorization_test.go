package kubebroker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewForwarderRefreshesCachedInventoryBeforeRevocation(t *testing.T) {
	now := time.Now()
	inventory := map[string]bool{"first": true}
	calls := 0
	fail := false
	cache := &AuthorizationCache{Now: func() time.Time { return now }, Fetch: func(context.Context) (map[string]bool, error) {
		calls++
		if fail {
			return nil, errors.New("unavailable")
		}
		copy := map[string]bool{}
		for id, allowed := range inventory {
			copy[id] = allowed
		}
		return copy, nil
	}}
	if ok, err := cache.Allowed(t.Context(), "first"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	inventory["second"] = true
	if ok, err := cache.Allowed(t.Context(), "second"); err != nil || !ok || calls != 2 {
		t.Fatal("new child incorrectly treated as revoked", ok, err, calls)
	}
	inventory["second"] = false
	now = now.Add(6 * time.Second)
	if ok, err := cache.Allowed(t.Context(), "second"); err != nil || ok {
		t.Fatal("revocation not enforced", ok, err)
	}
	fail = true
	now = now.Add(6 * time.Second)
	if ok, err := cache.Allowed(t.Context(), "first"); err != nil || !ok {
		t.Fatal("known authorization interrupted during bounded control outage", ok, err)
	}
	if ok, err := cache.Allowed(t.Context(), "unknown"); err == nil || ok {
		t.Fatal("unknown identity allowed offline")
	}
	now = now.Add(5 * time.Minute)
	if ok, err := cache.Allowed(t.Context(), "first"); err == nil || ok {
		t.Fatal("stale authorization survived expiry")
	}
}
