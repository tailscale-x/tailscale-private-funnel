package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIngressReplacementIsAdministratorOnly(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("old", "ingress", "old-ingress")
	_ = s.UpsertNode("new", "ingress", "new-ingress")
	_ = s.BindNodeIdentity("new", "key", "new-device", nil)
	_ = s.RevokeNode("old")
	for _, id := range []string{"old", "new"} {
		_ = s.SetJSON("node_deployment:"+id, nodeDeployment{})
	}
	r := WithPrincipal(httptest.NewRequest("POST", "/api/nodes/old/replace", strings.NewReader(`{"replacement_id":"new"}`)), Principal{NodeID: "viewer"})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w = requestManagement(c, "POST", "/api/nodes/old/replace", map[string]any{"replacement_id": "new"}, "owner"); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = s.RevokeNode("new")
	if w = requestManagement(c, "POST", "/api/nodes/old/replace", map[string]any{"replacement_id": "new"}, "owner"); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
