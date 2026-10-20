package gateway

import "time"

// BackendContinuityGrace bounds retention of an already authorized listener
// during an EndpointSlice gap or failed reachability check. Lease fencing and
// authoritative configuration removal always take precedence.
const BackendContinuityGrace = 2 * time.Minute

type BackendContinuity struct{ unavailableSince time.Time }

func (c *BackendContinuity) Keep(existing, reachable bool, now time.Time) bool {
	if reachable {
		c.unavailableSince = time.Time{}
		return true
	}
	if !existing {
		c.unavailableSince = time.Time{}
		return false
	}
	if c.unavailableSince.IsZero() {
		c.unavailableSince = now
	}
	return now.Sub(c.unavailableSince) < BackendContinuityGrace
}
