package web

import (
	"context"
	"hash/fnv"
)

func (c *Control) lockReport(ctx context.Context, id string) (func(), error) {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	m := &c.reportLocks[h.Sum64()%uint64(len(c.reportLocks))]
	if err := m.Lock(ctx); err != nil {
		return nil, err
	}
	return m.Unlock, nil
}
