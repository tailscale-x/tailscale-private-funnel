package store

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDatabaseContractTunnelBootstrapBudget(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			now := time.Unix(1800000000, 0)
			for i := 0; i < 5; i++ {
				if err := s.ReserveTunnelBootstrap("member", now); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ReserveTunnelBootstrap("member", now); !errors.Is(err, ErrTunnelBootstrapRate) {
				t.Fatal(err)
			}
			// The exhausted per-device attempt must not consume the global slot.
			for i := 0; i < 25; i++ {
				if err := s.ReserveTunnelBootstrap(string(rune('a'+i)), now); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ReserveTunnelBootstrap("new", now); !errors.Is(err, ErrTunnelBootstrapRate) {
				t.Fatal(err)
			}
			if err := s.ReserveTunnelBootstrap("member", now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDatabaseContractTunnelBootstrapConcurrentBudget(t *testing.T) {
	for _, postgres := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[postgres], func(t *testing.T) {
			s, _, _ := contractStore(t, postgres)
			if postgres {
				s.db.SetMaxOpenConns(16)
			}
			now := time.Unix(1800000000, 0)
			var wg sync.WaitGroup
			var accepted atomic.Int32
			for i := 0; i < 48; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					err := s.ReserveTunnelBootstrap(fmt.Sprintf("device-%d", i), now)
					if err == nil {
						accepted.Add(1)
					} else if !errors.Is(err, ErrTunnelBootstrapRate) {
						t.Errorf("reservation failed: %v", err)
					}
				}(i)
			}
			wg.Wait()
			if accepted.Load() != 30 {
				t.Fatalf("global budget accepted %d requests, want 30", accepted.Load())
			}
		})
	}
}
