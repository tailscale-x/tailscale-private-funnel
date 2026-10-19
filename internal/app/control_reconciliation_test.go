package app

import (
	"context"
	"testing"
	"time"
)

type blockedDNSControl struct {
	started        chan struct{}
	cleanupStarted chan struct{}
	release        chan struct{}
	approved       chan error
	expired        chan error
	endpoints      chan error
}

func (c *blockedDNSControl) ReconcilePolicy(ctx context.Context) error {
	select {
	case c.started <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *blockedDNSControl) RetryNodeCleanup(ctx context.Context) {
	select {
	case c.cleanupStarted <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
	case <-ctx.Done():
	}
}
func (c *blockedDNSControl) ExpireReports(ctx context.Context, _ time.Duration) (int, error) {
	select {
	case c.expired <- ctx.Err():
	default:
	}
	return 0, nil
}
func (*blockedDNSControl) CleanupStaleNodes(context.Context, time.Duration) (int, error) {
	return 0, nil
}
func (c *blockedDNSControl) ReconcileLoadBalancerDNS(ctx context.Context) error {
	select {
	case c.endpoints <- ctx.Err():
	default:
	}
	return nil
}
func (c *blockedDNSControl) ReconcileServiceApprovals(ctx context.Context) error {
	select {
	case c.approved <- ctx.Err():
	default:
	}
	return ctx.Err()
}
func TestServiceApprovalDoesNotWaitForDNSReconciliation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &blockedDNSControl{started: make(chan struct{}, 1), cleanupStarted: make(chan struct{}, 1), release: make(chan struct{}), approved: make(chan error, 8), expired: make(chan error, 8), endpoints: make(chan error, 8)}
	defer close(c.release)
	startControlReconciliation(ctx, c, 5*time.Millisecond, 5*time.Second)
	select {
	case <-c.started:
	case <-time.After(time.Second):
		t.Fatal("DNS pass did not start")
	}
	select {
	case <-c.cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("cleanup pass did not start")
	}
	// DNS and revoked-node cleanup are still blocked. Service approval must run repeatedly with live contexts.
	for i := 0; i < 2; i++ {
		for name, result := range map[string]<-chan error{"approval": c.approved, "expiry": c.expired, "endpoints": c.endpoints} {
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("blocked DNS starved " + name)
			}
		}
	}
}

func (*blockedDNSControl) ReconcileDockerRetirements(context.Context) error { return nil }
