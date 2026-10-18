package gateway

import "testing"

func TestNamedRouteLabels(t *testing.T) {
	labels := map[string]string{
		ExposureLabel: "8000:app.example.com:http",
		"tailscale-private-funnel.routes.api.hostname":       "app.example.com",
		"tailscale-private-funnel.routes.api.port":           "8080",
		"tailscale-private-funnel.routes.api.path-prefix":    "/api/",
		"tailscale-private-funnel.routes.api.replace-prefix": "/",
		"tailscale-private-funnel.routes.admin.hostname":     "app.example.com",
		"tailscale-private-funnel.routes.admin.port":         "9090",
		"tailscale-private-funnel.routes.admin.path-prefix":  "/admin",
	}
	specs, err := ParseLabels(labels, nil)
	if err != nil || len(specs) != 3 {
		t.Fatal(specs, err)
	}
	if specs[2].PathPrefix != "/api" || specs[2].Rewrite.ReplacePrefix != "/" {
		t.Fatal(specs)
	}
	labels["vendor.routes.api.port"] = "9999"
	if _, err = ParseLabels(labels, []string{"vendor.expose"}); err == nil {
		t.Fatal("conflicting alias accepted")
	}
	delete(labels, "vendor.routes.api.port")
	labels["tailscale-private-funnel.routes.admin.path-prefix"] = "/api"
	if _, err = ParseLabels(labels, nil); err == nil {
		t.Fatal("duplicate path accepted")
	}
}
func TestNamedRouteRejectsInvalidFields(t *testing.T) {
	for _, field := range []string{"path-prefix", "port", "hostname"} {
		labels := map[string]string{"tailscale-private-funnel.routes.a.hostname": "app.example.com", "tailscale-private-funnel.routes.a.port": "8080", "tailscale-private-funnel.routes.a.path-prefix": "/api"}
		delete(labels, "tailscale-private-funnel.routes.a."+field)
		if _, err := ParseLabels(labels, nil); err == nil {
			t.Fatal("missing", field)
		}
	}
	for _, value := range []string{"/x?query", "/x#fragment", "/{path}", "/$1", ""} {
		labels := map[string]string{"tailscale-private-funnel.routes.a.hostname": "app.example.com", "tailscale-private-funnel.routes.a.port": "8080", "tailscale-private-funnel.routes.a.path-prefix": "/api", "tailscale-private-funnel.routes.a.replace-prefix": value}
		if _, err := ParseLabels(labels, nil); err == nil {
			t.Fatal("invalid rewrite accepted", value)
		}
	}
}
