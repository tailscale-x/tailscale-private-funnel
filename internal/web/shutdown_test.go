package web

import (
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

func TestShutdownReleasesOnlyTheComponentAssignment(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("self", "tailnet-gateway", "self")
	if e := bindTestNode(s, "self", "shared-device", nil); e != nil {
		t.Fatal(e)
	}
	client := nodeClient(t, c, Principal{NodeID: "shared-device", Tagged: true})
	if _, e := client.Shutdown(t.Context(), connect.NewRequest(&v1.ShutdownRequest{})); e != nil {
		t.Fatal(e)
	}
	n, e := s.Node("self")
	if e != nil || n.Revoked || n.TailnetID != "shared-device" {
		t.Fatal("ordinary stop changed identity authority", n, e)
	}
	if _, e = client.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("stopped instance kept assignment", e)
	}
}
