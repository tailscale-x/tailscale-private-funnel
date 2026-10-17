package web

import (
	"testing"
)

func TestExplicitAdminSelectorsOverrideDefaultOwner(t *testing.T) {
	settings := AccessSettings{Roles: map[string][]string{"admin": {"autogroup:admin"}}}
	if settings.permissions(Principal{TailnetRole: "owner"}).Admin {
		t.Fatal("owner bypassed configured roles")
	}
	if !settings.permissions(Principal{TailnetRole: "admin"}).Admin {
		t.Fatal("explicit admin role denied")
	}
	if settings.permissions(Principal{TailnetRole: "admin", Tagged: true}).Admin {
		t.Fatal("tagged identity inherited human role")
	}
	if (AccessSettings{Roles: map[string][]string{"admin": {}}}).permissions(Principal{TailnetRole: "owner"}).Admin {
		t.Fatal("explicitly empty mapping ignored")
	}
}
