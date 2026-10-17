package kubebroker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestControlOutageDoesNotBreakOwnedLeaseOrRelaxPodAuthentication(t *testing.T) {
	b, status := fixture(t)
	now := time.Now()
	b.Now = func() time.Time { return now }
	outage := false
	cache := &AuthorizationCache{Now: b.Now, Fetch: func(context.Context) (map[string]bool, error) {
		if outage {
			return nil, errors.New("control offline")
		}
		return map[string]bool{"child": true}, nil
	}}
	b.Allowed = cache.Allowed
	if r := call(b, "/funnel.v1.BrokerService/OpenSession", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := call(b, "/funnel.v1.BrokerService/RenewLease", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	outage = true
	for i := 0; i < 7; i++ {
		now = now.Add(15 * time.Second)
		if r := call(b, "/funnel.v1.BrokerService/RenewLease", ""); r.Code != 200 {
			t.Fatalf("lease interrupted during control update: %s", r.Body.String())
		}
	}
	status.User.Extra["authentication.kubernetes.io/pod-uid"] = []string{"spoofed"}
	if r := call(b, "/funnel.v1.BrokerService/RenewLease", ""); r.Code != 403 {
		t.Fatal("cached authorization bypassed Pod UID validation")
	}
	status.User.Extra["authentication.kubernetes.io/pod-uid"] = []string{"pod-a"}
	now = now.Add(5 * time.Minute)
	if r := call(b, "/funnel.v1.BrokerService/RenewLease", ""); r.Code == 200 {
		t.Fatal("lease renewed beyond control authorization freshness")
	}
}
