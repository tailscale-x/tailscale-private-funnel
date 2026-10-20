package app

import (
	"context"
	"errors"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"sync/atomic"
	"testing"
	"time"
)

type leaseFixture struct {
	binding       *v1.BrokerSession
	fail          bool
	renews, opens int
	openCheck     func()
}

func (f *leaseFixture) RenewLease(context.Context) error {
	f.renews++
	if f.fail {
		return errors.New("API stalled")
	}
	return nil
}
func (f *leaseFixture) OpenSession(context.Context) (*v1.BrokerSession, error) {
	f.opens++
	if f.openCheck != nil {
		f.openCheck()
	}
	if f.fail {
		return nil, errors.New("API stalled")
	}
	return f.binding, nil
}

type fixtureFencer struct {
	fenced          bool
	fences, resumes int
}

func (m *fixtureFencer) Fence()  { m.fenced = true; m.fences++ }
func (m *fixtureFencer) Resume() { m.fenced = false; m.resumes++ }
func leaseTestFixture(t *testing.T) (*forwarderLease, *leaseFixture, *fixtureFencer, *time.Time) {
	t.Helper()
	now := time.Unix(1000, 0)
	b := &v1.BrokerSession{ControllerId: "controller", NodeId: "forwarder", NodeUid: "node-uid", NodeName: "node-name", ManagementUrl: "http://control:8080"}
	c := &leaseFixture{binding: b}
	var active atomic.Bool
	l := newForwarderLease(c, b, &active, func() time.Time { return now })
	m := &fixtureFencer{}
	l.attach(m)
	return l, c, m, &now
}
func TestForwarderLeaseFencesBeforeExpiryAndRecoversSameIdentity(t *testing.T) {
	l, c, m, now := leaseTestFixture(t)
	c.fail = true
	for _, sec := range []int{15, 30} {
		*now = time.Unix(1000+int64(sec), 0)
		if l.step(t.Context()) == nil {
			t.Fatal("renewal failure hidden")
		}
		if m.fenced {
			t.Fatal("fenced before grace")
		}
	}
	*now = time.Unix(1040, 0)
	if l.step(t.Context()) == nil {
		t.Fatal("broker failure hidden")
	}
	if !m.fenced || l.active.Load() || m.fences != 1 {
		t.Fatal("listeners not fenced before server lease expiry")
	}
	// Repeated API failure keeps the same process, identity binding, and fence.
	*now = time.Unix(1120, 0)
	_ = l.step(t.Context())
	if m.fences != 1 || m.resumes != 0 {
		t.Fatal("repeated fence changed advertisements")
	}
	c.fail = false
	c.openCheck = func() {
		if !m.fenced {
			t.Fatal("advertised before reacquiring lease")
		}
	}
	*now = time.Unix(1125, 0)
	if err := l.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.fenced || !l.active.Load() || m.resumes != 1 || l.binding != c.binding {
		t.Fatal("same binding not resumed")
	}
	*now = time.Unix(1140, 0)
	if err := l.step(t.Context()); err != nil {
		t.Fatal(err)
	}
	if m.resumes != 1 {
		t.Fatal("ordinary renewal changed advertisements")
	}
}
func TestForwarderLeaseRejectsChangedAssignment(t *testing.T) {
	for _, field := range []string{"controller", "node", "uid", "name", "url"} {
		t.Run(field, func(t *testing.T) {
			l, c, m, now := leaseTestFixture(t)
			changed := &v1.BrokerSession{ControllerId: "controller", NodeId: "forwarder", NodeUid: "node-uid", NodeName: "node-name", ManagementUrl: "http://control:8080"}
			switch field {
			case "controller":
				changed.ControllerId = "other"
			case "node":
				changed.NodeId = "other"
			case "uid":
				changed.NodeUid = "other"
			case "name":
				changed.NodeName = "other"
			case "url":
				changed.ManagementUrl = "http://other"
			}
			c.binding = changed
			*now = time.Unix(1040, 0)
			if err := l.step(t.Context()); err == nil || !m.fenced || l.active.Load() {
				t.Fatal("different broker binding acquired old identity")
			}
		})
	}
}
func TestForwarderLeaseFencesListenersAttachedDuringOutage(t *testing.T) {
	l, c, _, now := leaseTestFixture(t)
	c.fail = true
	*now = time.Unix(1040, 0)
	_ = l.step(t.Context())
	m := &fixtureFencer{}
	l.attach(m)
	if !m.fenced {
		t.Fatal("late listener startup escaped fence")
	}
}
