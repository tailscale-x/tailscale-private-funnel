package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadViewIsOperationScopedAndCancellationBoundsPoolWait(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SetJSON("node_health:test", 1); err != nil {
		t.Fatal(err)
	}
	v := s.ReadView(context.Background())
	var value int
	if err = v.GetJSON("node_health:test", &value); err != nil || value != 1 {
		t.Fatal(value, err)
	}
	if err = s.SetJSON("node_health:test", 2); err != nil {
		t.Fatal(err)
	}
	if err = v.GetJSON("node_health:test", &value); err != nil || value != 1 {
		t.Fatal("view changed within operation", value, err)
	}
	if err = s.ReadView(context.Background()).GetJSON("node_health:test", &value); err != nil || value != 2 {
		t.Fatal("view leaked across requests", value, err)
	}
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Release eventually even if a regression ignores the deadline.
	timer := time.AfterFunc(time.Second, func() { _ = conn.Close() })
	defer timer.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = s.WithContext(ctx).GetJSON("node_health:test", &value)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatal("pool wait ignored request deadline", err, time.Since(start))
	}
}

func TestDatabaseContractLeaseRenewalAvoidsRedundantWrites(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			now := time.Now().Truncate(time.Second)
			r := Registration{ComponentID: "component", InstanceID: "process", DeviceID: "device", Installation: "main", Mode: "docker-gateway", Name: "Gateway", Connection: "host"}
			if _, err := s.RegisterComponent(r, now); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 15; i++ {
				if err := s.RenewInstance(r.ComponentID, r.InstanceID, r.DeviceID, now.Add(time.Duration(i)*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			var until int64
			if err := s.db.QueryRow("SELECT lease_until FROM component_instances WHERE component_id=?", r.ComponentID).Scan(&until); err != nil || until != now.Add(time.Minute).Unix() {
				t.Fatal("lease rewritten on every request", until, err)
			}
			if err := s.RenewInstance(r.ComponentID, r.InstanceID, r.DeviceID, now.Add(15*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow("SELECT lease_until FROM component_instances WHERE component_id=?", r.ComponentID).Scan(&until); err != nil || until != now.Add(75*time.Second).Unix() {
				t.Fatal("lease not renewed", until, err)
			}
			if err := s.RenewInstance(r.ComponentID, r.InstanceID, "wrong-device", now.Add(16*time.Second)); err == nil {
				t.Fatal("wrong device authorized by renewal fast path")
			}
		})
	}
}
