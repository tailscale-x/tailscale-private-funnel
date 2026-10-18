package app

import (
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
	"os"
	"path/filepath"
	"tailscale.com/ipn"
	"testing"
)

func TestPostgresKubernetesStorageDoesNotRequirePVC(t *testing.T) {
	o, e := kubernetesNodeOptions("control", Options{IdentitySecret: "control-identity", KubeNamespace: "system", Database: store.DatabaseOptions{Type: "postgres"}, StateDir: filepath.Join(t.TempDir(), "state"), DataDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	if o.DatabaseDir != "" || o.PersistentDir != "" {
		t.Fatal("PostgreSQL requires PVC", o)
	}
	if e = prepareNodeStorage("control", o); e != nil {
		t.Fatal(e)
	}
}
func TestPostgresMissingConnectionFailsClosed(t *testing.T) {
	// Public plans enforce control-only options; Kubernetes node restoration also
	// keeps its identity state separate from externally managed database material.
	o := Options{DataDir: t.TempDir(), StateDir: t.TempDir()}
	if _, e := openRuntimeStore(Options{DataDir: o.DataDir, Database: store.DatabaseOptions{Type: "postgres"}}); e == nil {
		t.Fatal("missing connection file accepted")
	}
}

// Run alongside database integration tests with a dedicated database URI.
func TestPostgresNodeBundleRoundTrip(t *testing.T) {
	uri := os.Getenv("FUNNEL_TEST_CONTROL_POSTGRES_URL")
	if uri == "" {
		t.Skip("dedicated PostgreSQL control test database required")
	}
	root := t.TempDir()
	urlfile := filepath.Join(root, "url")
	os.WriteFile(urlfile, []byte(uri), 0600)
	o := Options{Database: store.DatabaseOptions{Type: "postgres", URLFile: urlfile, TLSMode: os.Getenv("FUNNEL_TEST_CONTROL_TLS_MODE"), CAFile: os.Getenv("FUNNEL_TEST_CONTROL_CA_FILE")}, DataDir: filepath.Join(root, "data"), StateDir: filepath.Join(root, "state"), Hostname: "postgres-control", Tailnet: "example.ts.net"}
	os.MkdirAll(o.StateDir, 0700)
	os.MkdirAll(o.DataDir, 0700)
	state, _ := json.Marshal(map[string][]byte{string(ipn.MachineKeyStateKey): []byte("test-machine")})
	os.WriteFile(filepath.Join(o.StateDir, "tailscaled.state"), state, 0600)
	os.WriteFile(filepath.Join(o.DataDir, "hostname"), []byte(o.Hostname), 0600)
	s, e := openRuntimeStore(o)
	if e != nil {
		t.Fatal(e)
	}
	s.SetSecret("test", "private")
	s.Close()
	data, e := exportNodeBundle("control", o)
	if e != nil {
		t.Fatal(e)
	}
	data["mode"] = []byte("control")
	data["initialized"] = []byte("1")
	originalMarker := string(data["storage-marker"])
	again, e := exportNodeBundle("control", o)
	if e != nil || string(again["storage-marker"]) != originalMarker {
		t.Fatal("bootstrap retry changed database binding", e)
	}
	o.StateDir = filepath.Join(root, "replacement-state")
	o.DataDir = filepath.Join(root, "replacement-data")
	os.MkdirAll(o.StateDir, 0700)
	os.MkdirAll(o.DataDir, 0700)
	restored, e := restoreNodeBundle("control", o, core.Secret{Data: data})
	if e != nil {
		t.Fatal(e)
	}
	s, e = openRuntimeStore(restored)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if v, e := s.GetSecret("test"); e != nil || v != "private" {
		t.Fatal("lost encrypted credential", e)
	}
	if _, e = os.Stat(filepath.Join(o.DataDir, "funnel.db")); !os.IsNotExist(e) {
		t.Fatal("created SQLite fallback")
	}
}
