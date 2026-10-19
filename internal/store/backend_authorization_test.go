package store

import "testing"

func TestEnrolledBackendCannotResurrectRevokedAssignment(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.UpsertNode("parent", "k8s-gateway", "parent"); e != nil {
		t.Fatal(e)
	}
	child, e := reserveTestForwarder(s, "parent", "kube-node", "node")
	if e != nil {
		t.Fatal(e)
	}
	assignment := BackendAssignment{ID: "assignment", NodeID: child.ID, ApplicationID: "app", BackendPort: 80}
	if e = s.UpsertRegisteredBackend(assignment); e != nil {
		t.Fatal(e)
	}
	s.RevokeNode("parent")
	if e = s.UpsertRegisteredBackend(assignment); e == nil {
		t.Fatal("revoked parent permitted backend overwrite")
	}
	assignment.ID = "new"
	if e = s.UpsertRegisteredBackend(assignment); e == nil {
		t.Fatal("revoked parent permitted backend creation")
	}
	assignment.NodeID = "unknown"
	if e = s.UpsertRegisteredBackend(assignment); e == nil {
		t.Fatal("unknown node permitted backend creation")
	}
}
