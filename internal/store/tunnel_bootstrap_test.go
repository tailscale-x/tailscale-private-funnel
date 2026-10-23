package store

import (
	"errors"
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
