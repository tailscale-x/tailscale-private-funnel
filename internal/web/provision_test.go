package web

import (
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestAccountRolesAreVerifiedAndDoNotApplyToTaggedNodes(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &Control{Store: s, roleUsers: []tailscale.User{{ID: "u1", LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "active"}}, rolesExpire: time.Now().Add(time.Minute)}
	for _, v := range []struct {
		p    Principal
		want string
	}{{Principal{UserID: "u1", Login: "owner@example.com"}, "owner"}, {Principal{UserID: "49961107773209184", Login: "owner@example.com"}, "owner"}, {Principal{UserID: "u1", Login: "spoof@example.com"}, ""}, {Principal{UserID: "u1", Login: "owner@example.com", Tagged: true}, ""}} {
		got := c.tailnetRole(t.Context(), v.p)
		if got != v.want {
			t.Fatalf("role=%q want=%q", got, v.want)
		}
	}
	for _, role := range []string{"owner", "admin", "member"} {
		a := (AccessSettings{}).permissions(Principal{TailnetRole: role})
		if a.Admin != (role == "owner") {
			t.Fatalf("role=%s access=%+v", role, a)
		}
	}
	if (AccessSettings{}).permissions(Principal{TailnetRole: "owner", Tagged: true}).Admin {
		t.Fatal("tagged node inherited owner")
	}
}

func TestTailnetOwnerRoleRejectsSuspendedAndSharedUsers(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, u := range []tailscale.User{
		{ID: "opaque-owner-id", LoginName: "owner@example.com", Role: "owner", Type: "shared", Status: "active"},
		{ID: "opaque-owner-id", LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "suspended"},
		{ID: "opaque-owner-id", LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "needs-approval"},
	} {
		c := &Control{Store: s, roleUsers: []tailscale.User{u}, rolesExpire: time.Now().Add(time.Minute)}
		if role := c.tailnetRole(t.Context(), Principal{UserID: "123456", Login: "owner@example.com"}); role != "" {
			t.Fatalf("granted role=%s for %+v", role, u)
		}
	}
}
