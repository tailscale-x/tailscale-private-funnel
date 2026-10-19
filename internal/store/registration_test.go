package store

import (
	"errors"
	"testing"
	"time"
)

func TestDatabaseContractLogicalComponentReplacement(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			now := time.Now()
			r := Registration{ComponentID: "component", InstanceID: "pod-1", DeviceID: "device-1", Installation: "main", Mode: "docker-gateway", Name: "Docker", Hostname: "host", Connection: "embedded"}
			a, e := s.RegisterComponent(r, now)
			if e != nil {
				t.Fatal(e)
			}
			r.InstanceID = "pod-2"
			r.DeviceID = "device-2"
			if _, e = s.RegisterComponent(r, now); !errors.Is(e, ErrInstanceConflict) {
				t.Fatal("active assignment overwritten", e)
			}
			b, e := s.RegisterComponent(r, now.Add(61*time.Second))
			if e != nil || b.Generation != a.Generation+1 {
				t.Fatal(b, e)
			}
			n, e := s.Node("component")
			if e != nil || n.TailnetID != "device-2" {
				t.Fatal(n, e)
			}
			if e = s.CheckInstance("component", "pod-1", "device-1", now.Add(62*time.Second)); e == nil {
				t.Fatal("old instance authorized")
			}
			r.Mode = "loadbalancer"
			if _, e = s.RegisterComponent(r, now.Add(62*time.Second)); e == nil {
				t.Fatal("component role replaced")
			}
		})
	}
}
func TestDatabaseContractSharedBorrowedDevice(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			for _, id := range []string{"docker", "edge"} {
				r := Registration{ComponentID: id, InstanceID: "process-" + id, DeviceID: "shared", Installation: "main", Mode: "docker-gateway", Name: id, Hostname: "host", Connection: "host"}
				if id == "edge" {
					r.Mode = "loadbalancer"
				}
				if _, e := s.RegisterComponent(r, time.Now()); e != nil {
					t.Fatal(e)
				}
				borrowed, e := s.BorrowedDevice(id)
				if e != nil || !borrowed {
					t.Fatal(e)
				}
			}
			if _, e := s.NodeByTailnetID("shared"); e == nil {
				t.Fatal("ambiguous selector accepted")
			}
		})
	}
}

func TestDatabaseContractInstanceRecoversAfterControlOutageWithoutStealing(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			now := time.Now()
			r := Registration{ComponentID: "gateway", InstanceID: "process", DeviceID: "device", Installation: "main", Mode: "docker-gateway", Name: "Gateway", Connection: "embedded"}
			if _, err := s.RegisterComponent(r, now); err != nil {
				t.Fatal(err)
			}
			later := now.Add(3 * time.Minute)
			if err := s.RenewInstance("gateway", "process", "device", later); err != nil {
				t.Fatal("unchanged verified holder could not recover", err)
			}
			if err := s.CheckInstance("gateway", "process", "device", later); err != nil {
				t.Fatal(err)
			}
			if err := s.RenewInstance("gateway", "process", "spoof", later); err == nil {
				t.Fatal("wrong device renewed")
			}
			r.InstanceID = "replacement"
			r.DeviceID = "other-device"
			if _, err := s.RegisterComponent(r, later.Add(61*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := s.RenewInstance("gateway", "process", "device", later.Add(62*time.Second)); err == nil {
				t.Fatal("replaced holder reacquired assignment")
			}
			if err := s.ReleaseInstance("gateway", r.InstanceID, r.DeviceID); err != nil {
				t.Fatal(err)
			}
			if err := s.RenewInstance("gateway", r.InstanceID, r.DeviceID, later.Add(63*time.Second)); err == nil {
				t.Fatal("explicitly stopped holder resumed")
			}
		})
	}
}

func TestDatabaseContractExistingNodeCannotChangeRoleOnFirstRegistration(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			if err := s.UpsertNode("existing", "docker-gateway", "Docker"); err != nil {
				t.Fatal(err)
			}
			r := Registration{ComponentID: "existing", InstanceID: "process", DeviceID: "device", Installation: "main", Mode: "loadbalancer", Name: "Edge", Connection: "embedded"}
			if _, err := s.RegisterComponent(r, time.Now()); err == nil {
				t.Fatal("existing node changed role during registration")
			}
			n, err := s.Node("existing")
			if err != nil || n.Mode != "docker-gateway" {
				t.Fatal(n, err)
			}
		})
	}
}
