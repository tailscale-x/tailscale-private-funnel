package app

import (
	"context"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
)

// Run before closing the process-owned Tailscale connection. Closing tsnet first
// would discard routes underneath streams which the listener is still draining.
func drainGatewayListeners(listeners *gateway.ListenerManager) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	listeners.CloseWait(ctx)
}
