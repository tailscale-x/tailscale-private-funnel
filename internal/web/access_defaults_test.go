package web

import (
	"database/sql"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestDefaultOwnerAccessDoesNotClaimFirstVisitor(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &Control{Store: s, rolesExpire: time.Now().Add(time.Minute), roleUsers: []tailscale.User{
		{LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "active"},
		{LoginName: "admin@example.com", Role: "admin", Type: "member", Status: "active"},
		{LoginName: "member@example.com", Role: "member", Type: "member", Status: "active"},
	}}
	settings, err := c.loadAccessSettings()
	if err != nil || len(settings.Admins) != 0 || strings.Join(settings.Roles["admin"], ",") != "autogroup:owner" {
		t.Fatalf("defaults=%+v err=%v", settings, err)
	}
	for _, tc := range []struct {
		p      Principal
		status int
	}{
		{Principal{NodeID: "member", UserID: "1", Login: "member@example.com"}, 403},
		{Principal{NodeID: "tagged", Login: "owner@example.com", Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true}, 403},
		{Principal{NodeID: "admin", UserID: "2", Login: "admin@example.com"}, 403},
		{Principal{NodeID: "spoof", Groups: []string{"autogroup:owner"}}, 403},
		{Principal{NodeID: "owner", UserID: "3", Login: "owner@example.com"}, 200},
	} {
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, WithPrincipal(httptest.NewRequest("GET", "/access", nil), tc.p))
		if w.Code != tc.status {
			t.Fatalf("principal=%+v status=%d body=%s", tc.p, w.Code, w.Body.String())
		}
		if tc.status == 200 && (!strings.Contains(w.Body.String(), `value="autogroup:owner"`) || strings.Contains(w.Body.String(), "Exact administrator identities")) {
			t.Fatal("role default missing from UI")
		}
	}
	var saved AccessSettings
	if err := s.GetJSON("access_roles", &saved); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("request claimed administrator: %+v err=%v", saved, err)
	}
}

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
