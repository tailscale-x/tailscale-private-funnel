package web

import (
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func bindTestNode(s *store.Store, id, device string, addresses []string) error {
	n, e := s.Node(id)
	if e != nil {
		return e
	}
	_, e = s.RegisterComponent(store.Registration{ComponentID: id, InstanceID: "test-" + id, DeviceID: device, Installation: "main", Mode: n.Mode, ParentID: n.ParentID, Name: n.Name, Hostname: n.Hostname, Connection: "embedded", Addresses: addresses}, time.Now())
	return e
}
func reserveTestForwarder(s *store.Store, parent, uid, host string) (store.NodeRecord, error) {
	id := parent + ":" + uid
	_, e := s.RegisterComponent(store.Registration{ComponentID: id, InstanceID: "test-" + id, DeviceID: "fixture-" + id, Installation: "main", Mode: "k8s-forwarder", Name: host, Hostname: host, Connection: "embedded", ParentID: parent}, time.Now())
	if e != nil {
		return store.NodeRecord{}, e
	}
	_ = s.ReleaseInstance(id, "test-"+id, "fixture-"+id)
	return s.Node(id)
}
