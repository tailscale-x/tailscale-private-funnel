package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
)

// ReadView is an operation-scoped memoized view of routing metadata. It must
// never be used for ownership mutation or retained across requests. Provider
// inventory and ledger reads deliberately remain uncached.
type readView struct {
	mu     sync.Mutex
	values map[string]any
	err    error
}
type readResult[T any] struct {
	value T
	err   error
}

func (s *Store) ReadView(ctx context.Context) *Store {
	v := s.WithContext(ctx)
	v.reads = &readView{values: map[string]any{}}
	return v
}
func (s *Store) ReadError() error {
	if s.reads == nil {
		return nil
	}
	s.reads.mu.Lock()
	defer s.reads.mu.Unlock()
	return s.reads.err
}
func readCached[T any](s *Store, key string, load func(*Store) (T, error)) (T, error) {
	s.reads.mu.Lock()
	defer s.reads.mu.Unlock()
	if v, ok := s.reads.values[key]; ok {
		r := v.(readResult[T])
		return r.value, r.err
	}
	base := &Store{db: s.db, key: s.key}
	v, err := load(base)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && s.reads.err == nil {
		s.reads.err = err
	}
	s.reads.values[key] = readResult[T]{v, err}
	return v, err
}
func (s *Store) cachedJSON(name string, out any) error {
	b, err := readCached(s, "json:"+name, func(base *Store) (json.RawMessage, error) {
		var b json.RawMessage
		err := base.GetJSON(name, &b)
		return b, err
	})
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, out)
	if err != nil {
		// Raw JSON can be syntactically valid but incompatible with the typed
		// metadata. That is a failed read, not an empty authorization decision.
		s.reads.mu.Lock()
		if s.reads.err == nil {
			s.reads.err = err
		}
		s.reads.mu.Unlock()
	}
	return err
}
