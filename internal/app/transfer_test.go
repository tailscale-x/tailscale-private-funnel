package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func testStoppedTransfer(t *testing.T) (Options, *ControlTransfer) {
	t.Helper()
	base := t.TempDir()
	o := Options{StateDir: filepath.Join(base, "state"), DataDir: filepath.Join(base, "data"), Hostname: "control-fixture", Tailnet: "example.ts.net"}
	os.MkdirAll(o.StateDir, 0700)
	os.MkdirAll(o.DataDir, 0700)
	st, err := store.Open(o.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSecret("tailscale_oauth_client_id", "fixture-client")
	st.SetSecret("tailscale_oauth_client_secret", "fixture-secret")
	st.ConfigurationKey()
	st.Close()
	state, _ := json.Marshal(map[string][]byte{"_machinekey": []byte("persistent-machine-key"), "_profiles": []byte("profile"), "profile-key": []byte("node identity and assigned IPs")})
	os.WriteFile(filepath.Join(o.StateDir, "tailscaled.state"), state, 0600)
	os.WriteFile(filepath.Join(o.StateDir, "joined"), []byte("persistent\n"), 0600)
	os.WriteFile(filepath.Join(o.DataDir, "hostname"), []byte(o.Hostname), 0600)
	b, err := ExportStoppedControl(context.Background(), o, nil)
	if err != nil {
		t.Fatal(err)
	}
	return o, b
}
func TestStoppedControlTransferPreservesIdentity(t *testing.T) {
	source, b := testStoppedTransfer(t)
	dir := t.TempDir()
	o := Options{StateDir: filepath.Join(dir, "state"), DataDir: filepath.Join(dir, "data")}
	if err := RestoreControl(context.Background(), o, b); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(source.StateDir, "tailscaled.state"))
	restored, _ := os.ReadFile(filepath.Join(o.StateDir, "tailscaled.state"))
	if !bytes.Equal(original, restored) {
		t.Fatal("Tailscale state changed")
	}
	st, err := store.Open(o.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if value, err := st.GetSecret("tailscale_oauth_client_secret"); err != nil || value != "fixture-secret" {
		t.Fatal("OAuth not restored", err)
	}
	if err := RestoreControl(context.Background(), o, b); err == nil {
		t.Fatal("duplicate restore accepted")
	}
}
func TestFailedControlRestoreIsFenced(t *testing.T) {
	_, b := testStoppedTransfer(t)
	b.Database.KeyHash = "wrong"
	dir := t.TempDir()
	o := Options{StateDir: filepath.Join(dir, "state"), DataDir: filepath.Join(dir, "data")}
	if err := RestoreControl(context.Background(), o, b); err == nil {
		t.Fatal("invalid restore accepted")
	}
	if _, err := os.Stat(filepath.Join(o.StateDir, "restore-incomplete")); err != nil {
		t.Fatal("missing failure fence", err)
	}
	if err := Serve(context.Background(), "control", o); err == nil {
		t.Fatal("failed restore started fresh identity")
	}
}

func TestControlStartupInitializesSigningIdentityBeforeJoinFailure(t *testing.T) {
	root := t.TempDir()
	o := Options{StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), Hostname: "unjoined-fixture"}
	if err := Serve(context.Background(), "control", o); err == nil {
		t.Fatal("missing OAuth accepted")
	}
	st, err := store.Open(o.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.GetSecret("control_configuration_key"); err != nil {
		t.Fatal("startup failed to establish signing identity", err)
	}
}
