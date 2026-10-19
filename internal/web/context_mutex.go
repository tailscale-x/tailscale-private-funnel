package web

import (
	"context"
	"sync"
)

// contextMutex cancels queued provider work before it consumes another pass's
// budget. In-flight ownership checks and mutations remain serialized.
type contextMutex struct {
	once sync.Once
	gate chan struct{}
}

func (m *contextMutex) Lock(ctx context.Context) error {
	m.once.Do(func() { m.gate = make(chan struct{}, 1) })
	select {
	case m.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *contextMutex) Unlock() { <-m.gate }
