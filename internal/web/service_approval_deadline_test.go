package web

import (
	"context"
	"errors"
	"testing"
)

func TestServiceApprovalRejectsExpiredContextBeforeLoadingCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Control{} // No store or API client should be consulted.
	if err := c.ReconcileServiceApprovals(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestConcurrentServiceApprovalDoesNotQueueBehindRunningPass(t *testing.T) {
	c := &Control{}
	c.serviceApprovalMu.Lock()
	defer c.serviceApprovalMu.Unlock()
	if err := c.ReconcileServiceApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
}
