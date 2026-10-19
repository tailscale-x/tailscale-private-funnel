package kubebroker

import (
	"context"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

// ConfigurationCache serves only still-valid authenticated control configurations.
// Outages never refresh their issue times or extend their original expiry.
type ConfigurationCache struct {
	Fetch  func(context.Context) (report.Snapshot, error)
	NodeID string
	Now    func() time.Time
	mu     sync.Mutex
	last   report.Snapshot
}

func (c *ConfigurationCache) Get(ctx context.Context) (report.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	env, err := c.Fetch(bounded)
	cancel()
	if err == nil {
		if _, err = nodeconfig.Read(env, c.NodeID, now); err == nil {
			c.last = env
			return env, nil
		}
		// Invalid data is not a transient outage.
		c.last = report.Snapshot{}
		return report.Snapshot{}, err
	}
	if !transientControlFailure(err) {
		c.last = report.Snapshot{}
		return report.Snapshot{}, err
	}
	if _, cachedErr := nodeconfig.Read(c.last, c.NodeID, now); cachedErr == nil {
		return c.last, nil
	}
	return report.Snapshot{}, err
}

func transientControlFailure(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnknown, connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeCanceled, connect.CodeResourceExhausted:
		return true
	default:
		return false
	}
}
