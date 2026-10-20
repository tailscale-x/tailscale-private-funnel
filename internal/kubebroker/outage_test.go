package kubebroker

import (
	"context"
	"errors"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"net/http"
	"net/http/httptest"
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

func TestSlowControlConfigurationDoesNotBlockLeaseRenewal(t *testing.T) {
	b, _ := fixture(t)
	if r := call(b, "/funnel.v1.BrokerService/OpenSession", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	b.Configuration = func(context.Context) (report.Snapshot, error) {
		close(started)
		<-release
		return report.Snapshot{}, errors.New("control API stalled")
	}
	go func() { defer close(finished); call(b, "/funnel.v1.BrokerService/GetConfig", "") }()
	<-started
	renew := make(chan int, 1)
	go func() { renew <- call(b, "/funnel.v1.BrokerService/RenewLease", "").Code }()
	select {
	case code := <-renew:
		close(release)
		<-finished
		if code != 200 {
			t.Fatalf("lease renewal returned %d during stalled config", code)
		}
	case <-time.After(time.Second):
		close(release)
		<-finished
		t.Fatal("control configuration blocked independent lease renewal")
	}
}

func TestControllerRegistrationDoesNotWithdrawBrokerOrExistingLease(t *testing.T) {
	b, status := fixture(t)
	sessionReady := true
	b.Ready = func() bool { return true }
	b.SessionReady = func() bool { return sessionReady }
	if r := call(b, "/funnel.v1.BrokerService/OpenSession", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	sessionReady = false
	if r := call(b, "/funnel.v1.BrokerService/OpenSession", ""); r.Code != 503 {
		t.Fatal("new session did not wait for registration", r.Code)
	}
	if r := call(b, "/funnel.v1.BrokerService/RenewLease", ""); r.Code != 200 {
		t.Fatal("registration outage blocked existing lease", r.Code)
	}
	r := httptest.NewRecorder()
	b.Handler().ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if r.Code != 200 {
		t.Fatal("broker Service endpoint withdrawn while lease API is available")
	}
	status.User.Extra["authentication.kubernetes.io/pod-uid"] = []string{"forged"}
	if r := call(b, "/funnel.v1.BrokerService/RenewLease", ""); r.Code != 403 {
		t.Fatal("registration outage bypassed Pod authentication")
	}
}
