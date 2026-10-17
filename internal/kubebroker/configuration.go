package kubebroker

import (
	"connectrpc.com/connect"
	"context"
	"sync"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

// ConfigurationCache serves only still-valid control-signed configurations.
// Outages never refresh their signatures or extend their original expiry.
type ConfigurationCache struct {
	Fetch             func(context.Context) (report.Envelope, error)
	PublicKey, NodeID string
	Now               func() time.Time
	mu                sync.Mutex
	last              report.Envelope
}

func (c *ConfigurationCache) Get(ctx context.Context) (report.Envelope, error) {
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
		if _, err = nodeconfig.Verify(env, c.PublicKey, c.NodeID, now); err == nil {
			c.last = env
			return env, nil
		}
		// Invalid signed data is not a transient outage.
		c.last = report.Envelope{}
		return report.Envelope{}, err
	}
	if !transientControlFailure(err) {
		c.last = report.Envelope{}
		return report.Envelope{}, err
	}
	if _, cachedErr := nodeconfig.Verify(c.last, c.PublicKey, c.NodeID, now); cachedErr == nil {
		return c.last, nil
	}
	return report.Envelope{}, err
}

func transientControlFailure(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodeUnknown, connect.CodeUnavailable, connect.CodeDeadlineExceeded, connect.CodeCanceled, connect.CodeResourceExhausted:
		return true
	default:
		return false
	}
}
