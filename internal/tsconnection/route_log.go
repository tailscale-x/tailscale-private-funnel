package tsconnection

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const routeLogTargets = 128

type routeFailure struct {
	since, lastLog time.Time
	attempts       uint64
}

// Route observations never decide readiness, modify routes, or retry a dial.
// Track failures only, with bounded cardinality and per-target/global limits.
type routeObserver struct {
	mu           sync.Mutex
	failures     map[string]routeFailure
	limit        *rate.Limiter
	logger       *slog.Logger
	snapshotBusy bool
}

func routeErrorClass(err error) string {
	switch {
	case errors.Is(err, ErrNoTailnetRoute):
		return "missing_route"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	default:
		var n net.Error
		if errors.As(err, &n) && n.Timeout() {
			return "timeout"
		}
		return "netstack_dial_failed"
	}
}

// observe reports the first failure, a minute-level summary, and recovery.
// Caller cancellations (including losing dual-stack racers) are not outages.
func (o *routeObserver) observe(ctx context.Context, network, target string, elapsed time.Duration, err error, now time.Time) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	key := network + ":" + target
	o.mu.Lock()
	defer o.mu.Unlock()
	prior, failed := o.failures[key]
	if err == nil && !failed {
		return false
	}
	if o.limit == nil {
		o.limit = rate.NewLimiter(rate.Every(time.Second), 4)
	}
	if o.failures == nil {
		o.failures = make(map[string]routeFailure)
	}
	if err != nil {
		if !failed {
			if len(o.failures) >= routeLogTargets {
				// Bounded diagnostics must not retain unbounded target state. Eviction
				// affects observations only; it never changes connections or ownership.
				var oldestKey string
				var oldest time.Time
				for k, v := range o.failures {
					if oldestKey == "" || v.since.Before(oldest) {
						oldestKey, oldest = k, v.since
					}
				}
				delete(o.failures, oldestKey)
			}
			prior.since = now
		}
		prior.attempts++
		o.failures[key] = prior
		if !prior.lastLog.IsZero() && now.Sub(prior.lastLog) < time.Minute {
			return false
		}
	} else {
		delete(o.failures, key)
	}
	if !o.limit.AllowN(now, 1) {
		return false
	}
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	attrs := []slog.Attr{slog.String("network", network), slog.String("target", target), slog.Duration("dial_duration", elapsed), slog.Duration("failure_duration", now.Sub(prior.since)), slog.Uint64("failed_attempts", prior.attempts)}
	if err != nil {
		prior.lastLog = now
		o.failures[key] = prior
		attrs = append(attrs, slog.String("error_class", routeErrorClass(err)))
		// The class, target and map snapshot are sufficient for correlation. Do
		// not log arbitrary error strings, URLs, headers, or request payloads here.
		logger.LogAttrs(ctx, slog.LevelWarn, "tailnet route dial failed", attrs...)
	} else {
		logger.LogAttrs(ctx, slog.LevelInfo, "tailnet route dial recovered", attrs...)
	}
	return true
}

// Sample metadata only; no active Ping, probe connection, or raw netmap output.
// At most one bounded background read per connection wrapper can be in flight.
func (e *Embedded) logRouteMap(target string) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !tailnetAddress(ip) {
		return
	}
	e.routes.mu.Lock()
	if e.routes.snapshotBusy {
		e.routes.mu.Unlock()
		return
	}
	e.routes.snapshotBusy = true
	e.routes.mu.Unlock()
	go func() {
		defer func() { e.routes.mu.Lock(); e.routes.snapshotBusy = false; e.routes.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		api, err := e.Server.LocalClient()
		if err != nil {
			slog.Warn("tailnet route map sample unavailable", "target", target)
			return
		}
		device, candidates, mapErr := readServiceMap(ctx, api, ip)
		slog.Info("tailnet route map sample", "target", target, "device_id", device, "candidates", candidates, "map_available", mapErr == nil)
	}()
}
