package app

import (
	"context"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"os"
	"testing"
)

func TestCleanupRejectsBootstrapEnvironmentBeforeOpeningState(t *testing.T) {
	for _, name := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TSNET_FORCE_LOGIN"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "not-used")
			if err := Cleanup(context.Background(), "docker-gateway", Options{}); err == nil {
				t.Fatal("cleanup accepted joining credentials")
			}
		})
	}
}
func TestCleanupConfirmationIsBoundToControlAndDurable(t *testing.T) {
	o := Options{DataDir: t.TempDir(), ManagementURL: "http://control:8080"}
	if cleanupConfirmed(o) {
		t.Fatal("unexpected receipt")
	}
	if err := persistCleanupConfirmation(o); err != nil {
		t.Fatal(err)
	}
	if !cleanupConfirmed(o) {
		t.Fatal("lost receipt")
	}
	s, err := os.Stat(o.DataDir + "/cleanup-confirmed")
	if err != nil || s.Mode().Perm() != 0600 {
		t.Fatal(s, err)
	}
	o.ManagementURL = "http://other:8080"
	if cleanupConfirmed(o) {
		t.Fatal("receipt crossed installations")
	}
}

func TestControlBootstrapPopulatesCanonicalURLAndPreservesOverrides(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = initializeControlManagement(st, "100.64.0.10", ":8080", "dev-example"); err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	st.GetJSON("tailnet", &settings)
	if settings["management_url"] != "http://100.64.0.10:8080" || settings["release_version"] != "dev-example" {
		t.Fatal(settings)
	}
	settings["management_url"] = "http://chosen.example.ts.net:9090"
	st.SetJSON("tailnet", settings)
	if err = initializeControlManagement(st, "100.64.0.20", ":8080", "dev-new"); err != nil {
		t.Fatal(err)
	}
	st.GetJSON("tailnet", &settings)
	if settings["management_url"] != "http://chosen.example.ts.net:9090" {
		t.Fatal("overrode admin URL", settings)
	}
}
