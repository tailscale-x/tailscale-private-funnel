package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistentBootstrapFailsClosed(t *testing.T) {
	o := Options{StateDir: filepath.Join(t.TempDir(), "state"), DataDir: filepath.Join(t.TempDir(), "data")}
	if validatePersistentState("control", o) == nil {
		t.Fatal("accepted missing state")
	}
	os.MkdirAll(o.StateDir, 0700)
	os.MkdirAll(o.DataDir, 0700)
	for _, f := range []string{filepath.Join(o.StateDir, "joined"), filepath.Join(o.StateDir, "tailscaled.state"), filepath.Join(o.DataDir, "funnel.db"), filepath.Join(o.DataDir, "master.key")} {
		os.WriteFile(f, []byte("invalid"), 0600)
	}
	if validatePersistentState("control", o) == nil {
		t.Fatal("accepted corrupt identity")
	}
	os.WriteFile(filepath.Join(o.StateDir, "tailscaled.state"), []byte(`{"profile":"c3RhdGU="}`), 0600)
	if err := validatePersistentState("control", o); err != nil {
		t.Fatal(err)
	}
	if validatePersistentState("loadbalancer", o) == nil {
		t.Fatal("accepted missing enrollment binding")
	}
}
