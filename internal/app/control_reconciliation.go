package app

import (
	"context"
	"log/slog"
	"time"
)

type periodicControl interface {
	RetryNodeCleanup(context.Context)
	ReconcilePolicy(context.Context) error
	ExpireReports(context.Context, time.Duration) (int, error)
	CleanupStaleNodes(context.Context, time.Duration) (int, error)
	ReconcileLoadBalancerDNS(context.Context) error
	ReconcileServiceApprovals(context.Context) error
}

// Service approval must keep progressing while provider reconciliation is slow.
// Each loop is serialized independently and receives a fresh timeout per pass.
func startControlReconciliation(ctx context.Context, control periodicControl, interval, budget time.Duration) {
	go reconcileEvery(ctx, interval, budget, func(pass context.Context) {
		_ = control.ReconcilePolicy(pass)
		if pass.Err() != nil {
			return
		}
		_, _ = control.ExpireReports(pass, 5*time.Minute)
		if pass.Err() != nil {
			return
		}
		_, _ = control.CleanupStaleNodes(pass, 5*time.Minute)
		if pass.Err() != nil {
			return
		}
		_ = control.ReconcileLoadBalancerDNS(pass)
	})
	go reconcileEvery(ctx, interval, budget, control.RetryNodeCleanup)
	go reconcileEvery(ctx, interval, budget, func(pass context.Context) {
		if err := control.ReconcileServiceApprovals(pass); err != nil && ctx.Err() == nil {
			slog.Warn("service approval reconciliation failed", "error", err)
		}
	})
}

func reconcileEvery(ctx context.Context, interval, budget time.Duration, run func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			pass, cancel := context.WithTimeout(ctx, budget)
			run(pass)
			cancel()
		}
	}
}
