package kubebroker

import (
	"context"
	"sync"
	"time"
)

// AuthorizationCache caches known children briefly. A miss always refreshes:
// another node may have enrolled since the last inventory was read.
// A known authorization survives a control outage for at most five minutes.
// Kubernetes token validation and identity-owner leases remain mandatory.
type AuthorizationCache struct {
	Fetch      func(context.Context) (map[string]bool, error)
	Now        func() time.Time
	mu         sync.Mutex
	checkedAt  time.Time
	inventory  map[string]bool
	retryAfter time.Time
}

func (c *AuthorizationCache) Allowed(ctx context.Context, id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	_, known := c.inventory[id]
	if known && now.Before(c.retryAfter) && now.Sub(c.checkedAt) < 5*time.Minute {
		return c.inventory[id], nil
	}
	if !known || now.Sub(c.checkedAt) > 5*time.Second {
		fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		inventory, err := c.Fetch(fetchCtx)
		cancel()
		if err != nil {
			if !transientControlFailure(err) {
				c.inventory = nil
				return false, err
			}
			c.retryAfter = now.Add(5 * time.Second)
			if known && now.Sub(c.checkedAt) < 5*time.Minute {
				return c.inventory[id], nil
			}
			return false, err
		}
		c.inventory = inventory
		c.checkedAt = now
		c.retryAfter = time.Time{}
	}
	return c.inventory[id], nil
}
