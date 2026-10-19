package kubebroker

import (
	"testing"
	"time"
)

func TestControlOutageDoesNotBreakOwnedLeaseOrRelaxPodAuthentication(t *testing.T) {
	b, status := fixture(t)
	now := time.Now()
	b.Now = func() time.Time { return now }
	if r := call(b, "/funnel.v1.BrokerService/OpenSession", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := call(b, "/funnel.v1.BrokerService/RenewLease", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
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
}
