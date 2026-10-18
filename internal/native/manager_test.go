package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func manager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	return &Manager{ConfigDir: filepath.Join(root, "config"), StateRoot: filepath.Join(root, "state"), BinaryRoot: filepath.Join(root, "bin"), UnitDir: filepath.Join(root, "units"), Run: func(context.Context, string, []string, io.Reader, []string) ([]byte, error) { return nil, nil }, Bootstrap: func(context.Context, *Resource, *v1.DeploymentInstructions, []string) error {
		return errors.New("test join failure")
	}}
}
func TestCreateReservesHandleAndNeverReplacesState(t *testing.T) {
	m := manager(t)
	r, e := m.Resolve("gateways", "site", "tailnet-gateway", "Label", "site", "dev-test", "", "")
	if e != nil {
		t.Fatal(e)
	}
	m.Create(t.Context(), r, nil, nil)
	if _, e = m.Create(t.Context(), r, nil, nil); e == nil {
		t.Fatal("second create claimed same handle")
	}
	b, _ := os.ReadFile(filepath.Join(r.StateDir, ".funnel-native-resource"))
	if string(b) != r.ID {
		t.Fatal("marker lost")
	}
	if _, e = m.Resolve("gateways", "site", "tailnet-gateway", "Label", "site", "dev-test", "", ""); e == nil {
		t.Fatal("duplicate resolved")
	}
}
func TestInspectionDoesNotOpenIdentity(t *testing.T) {
	m := manager(t)
	r, e := m.Resolve("controls", "main", "control", "Main", "main", "dev-test", "", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = m.save(&r); e != nil {
		t.Fatal(e)
	}
	m.Run = func(_ context.Context, p string, _ []string, _ io.Reader, _ []string) ([]byte, error) {
		if p != "systemctl" {
			t.Fatal("unexpected process", p)
		}
		return []byte("inactive"), nil
	}
	got, e := m.Get(t.Context(), "controls", "main")
	if e != nil || got.Phase != "Stopped" {
		t.Fatal(got, e)
	}
	if _, e = os.Stat(r.StateDir); !os.IsNotExist(e) {
		t.Fatal("inspection created identity")
	}
}
func TestDeleteRejectsWrongOwnershipBeforeStopping(t *testing.T) {
	m := manager(t)
	r, _ := m.Resolve("gateways", "site", "tailnet-gateway", "", "site", "dev-test", "", "")
	m.save(&r)
	m.Run = func(context.Context, string, []string, io.Reader, []string) ([]byte, error) {
		return []byte("inactive"), nil
	}
	e := m.Delete(t.Context(), "gateways", "site")
	if e == nil || !strings.Contains(e.Error(), "ownership") {
		t.Fatal(e)
	}
	if _, e = os.Stat(m.path("gateways", "site")); e != nil {
		t.Fatal("registry removed")
	}
}

func TestControlDeletionRefusesBeforeStoppingLiveManagement(t *testing.T) {
	m := manager(t)
	r, e := m.Resolve("controls", "main", "control", "Main", "main", "dev-test", "", "")
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		os.MkdirAll(p, 0700)
		os.WriteFile(filepath.Join(p, ".funnel-native-resource"), []byte(r.ID), 0600)
	}
	st, e := store.Open(r.DataDir)
	if e != nil {
		t.Fatal(e)
	}
	st.UpsertNode("active", "tailnet-gateway", "active")
	st.Close()
	m.save(&r)
	stopped := false
	m.Run = func(_ context.Context, p string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if p == "systemctl" && len(args) > 0 && args[0] == "stop" {
			stopped = true
		}
		return []byte("active"), nil
	}
	if e = m.Delete(t.Context(), "controls", "main"); e == nil || !strings.Contains(e.Error(), "active enrolled") {
		t.Fatal(e)
	}
	if stopped {
		t.Fatal("control stopped before deletion prerequisites passed")
	}
}

func TestGatewayReadinessRequiresReportFromCurrentProcess(t *testing.T) {
	m := manager(t)
	r, _ := m.Resolve("gateways", "site", "tailnet-gateway", "", "site", "dev-test", "", "")
	os.MkdirAll(r.DataDir, 0700)
	m.save(&r)
	os.WriteFile(filepath.Join(r.DataDir, "serve.pid"), []byte("42"), 0600)
	os.WriteFile(filepath.Join(r.DataDir, "runtime-status.json"), []byte(`{"state":"Running","ready":false}`), 0600)
	reportPath := filepath.Join(r.DataDir, "last-control-report")
	os.WriteFile(reportPath, []byte(fmt.Sprint(time.Now().Unix())), 0600)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(reportPath, old, old)
	m.Run = func(_ context.Context, _ string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if args[0] == "is-active" {
			return []byte("active"), nil
		}
		return []byte("42"), nil
	}
	getReady := func() bool {
		got, e := m.Get(t.Context(), "gateways", "site")
		if e != nil {
			t.Fatal(e)
		}
		var status struct{ Ready bool }
		json.Unmarshal(got.Runtime, &status)
		return status.Ready
	}
	if getReady() {
		t.Fatal("accepted previous process report")
	}
	os.WriteFile(reportPath, []byte(fmt.Sprint(time.Now().Unix())), 0600)
	if !getReady() {
		t.Fatal("fresh enrolled process not ready")
	}
}

func TestFailedCreateCanDeleteWithoutInstalledUnit(t *testing.T) {
	m := manager(t)
	r, _ := m.Resolve("gateways", "failed", "tailnet-gateway", "", "failed", "dev-test", "", "")
	m.Create(t.Context(), r, nil, nil)
	m.Run = func(_ context.Context, _ string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if args[0] == "show" {
			return []byte("not-found"), errors.New("unit absent")
		}
		return nil, nil
	}
	if e := m.Delete(t.Context(), "gateways", "failed"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(m.path("gateways", "failed")); !os.IsNotExist(e) {
		t.Fatal("failed registry retained", e)
	}
}

func TestCreateWithPrivateUmaskKeepsManagedParentsTraversable(t *testing.T) {
	old := syscall.Umask(0077)
	defer syscall.Umask(old)
	m := manager(t)
	state := filepath.Join(m.StateRoot, "gateways", "site", "state")
	data := filepath.Join(m.StateRoot, "gateways", "site", "data")
	r, e := m.Resolve("gateways", "site", "tailnet-gateway", "", "site", "dev-test", state, data)
	if e != nil {
		t.Fatal(e)
	}
	m.Create(t.Context(), r, nil, nil)
	for _, path := range []string{m.StateRoot, filepath.Join(m.StateRoot, "gateways"), filepath.Dir(state)} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != 0711 {
			t.Fatal(path, info, e)
		}
	}
	for _, path := range []string{state, data} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != 0700 {
			t.Fatal(path, info, e)
		}
	}
}

func TestJoinedNodeCleanupUsesItsRuntimeAccount(t *testing.T) {
	m := manager(t)
	r, _ := m.Resolve("gateways", "failed", "tailnet-gateway", "", "failed", "dev-test", "", "")
	m.Create(t.Context(), r, nil, nil)
	os.WriteFile(filepath.Join(r.StateDir, "joined"), []byte("persistent"), 0600)
	called := false
	m.Run = func(_ context.Context, program string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if program == "systemctl" && args[0] == "show" {
			return []byte("not-found"), nil
		}
		if program == "runuser" && strings.Contains(strings.Join(args, " "), "--cleanup-only") {
			called = true
			if args[0] != "--user" || args[1] != "tailscale-private-funnel" {
				t.Fatal("cleanup ran under a different account", args)
			}
		}
		return nil, nil
	}
	if e := m.Delete(t.Context(), "gateways", "failed"); e != nil {
		t.Fatal(e)
	}
	if !called {
		t.Fatal("cleanup did not use the runtime account")
	}
}

func TestControlInspectionReturnsPrivateSetupURL(t *testing.T) {
	m := manager(t)
	r, _ := m.Resolve("controls", "main", "control", "", "main", "dev-test", "", "")
	os.MkdirAll(r.DataDir, 0700)
	m.save(&r)
	os.WriteFile(filepath.Join(r.DataDir, "serve.pid"), []byte("42"), 0600)
	os.WriteFile(filepath.Join(r.DataDir, "runtime-status.json"), []byte(`{"ready":true,"state":"Running","ips":["100.64.0.10"]}`), 0600)
	m.Run = func(_ context.Context, _ string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if args[0] == "is-active" {
			return []byte("active"), nil
		}
		return []byte("42"), nil
	}
	got, e := m.Get(t.Context(), "controls", "main")
	if e != nil || got.SetupURL != "http://100.64.0.10:8080" {
		t.Fatal(got, e)
	}
}

func TestInspectionRejectsStoredRuntimeFromAnOlderProcess(t *testing.T) {
	m := manager(t)
	r, _ := m.Resolve("controls", "main", "control", "", "main", "dev-test", "", "")
	os.MkdirAll(r.DataDir, 0700)
	r.Runtime = []byte(`{"ready":true,"state":"Running","ips":["100.64.0.10"]}`)
	r.SetupURL = "http://100.64.0.10:8080"
	m.save(&r)
	os.WriteFile(filepath.Join(r.DataDir, "serve.pid"), []byte("41"), 0600)
	os.WriteFile(filepath.Join(r.DataDir, "runtime-status.json"), r.Runtime, 0600)
	m.Run = func(_ context.Context, _ string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if args[0] == "is-active" {
			return []byte("active"), nil
		}
		return []byte("42"), nil
	}
	got, e := m.Get(t.Context(), "controls", "main")
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Runtime) != 0 || got.SetupURL != "" {
		t.Fatal("accepted stale registry liveness", got)
	}
}

func TestPostgresNativeCredentialsRemainInProtectedFiles(t *testing.T) {
	m := manager(t)
	os.MkdirAll(m.UnitDir, 0755)
	r, e := m.Resolve("controls", "postgres", "control", "Postgres", "postgres", "dev-test", "", "")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "database.url")
	if e = os.WriteFile(path, []byte("postgres://user:private-password@db/app"), 0600); e != nil {
		t.Fatal(e)
	}
	p := &v1.DeploymentInstructions{Deployment: &v1.DeploymentRequest{Mode: "control", Database: &v1.DatabaseConfiguration{Type: "postgres", UrlFile: path, TlsMode: "verify-full"}}}
	m.Run = func(_ context.Context, _ string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "private-password") {
			t.Fatal("password in argv")
		}
		return nil, nil
	}
	m.Bootstrap = func(_ context.Context, r *Resource, _ *v1.DeploymentInstructions, _ []string) error {
		if r.Database.URLFile == path {
			t.Fatal("must copy connection into owned storage")
		}
		b, e := os.ReadFile(r.Database.URLFile)
		if e != nil || !strings.Contains(string(b), "private-password") {
			t.Fatal(e)
		}
		return nil
	}
	got, e := m.Create(t.Context(), r, p, nil)
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(m.path(got.Kind, got.Name))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "private-password") {
		t.Fatal("password in receipt")
	}
	unit, e := os.ReadFile(filepath.Join(m.UnitDir, got.Unit))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(unit), "--database-url-file") || strings.Contains(string(unit), "private-password") {
		t.Fatal("incorrect unit")
	}
}

func TestInstallMakesVersionDirectoryTraversableBeforeInstallingBinary(t *testing.T) {
	m := manager(t)
	r, err := m.Resolve("gateways", "site", "tailnet-gateway", "Site", "site", "dev-permissions", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(m.UnitDir, 0700); err != nil {
		t.Fatal(err)
	}
	m.Binary = "/tmp/test-funnel-binary"
	for _, dir := range []string{r.StateDir, r.DataDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	prepared := false
	m.Run = func(_ context.Context, command string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		if command == "install" && len(args) > 0 {
			if args[0] == "-d" {
				want := "-d -o root -g root -m 0755 " + filepath.Join(m.BinaryRoot, r.Version)
				if strings.Join(args, " ") != want {
					t.Fatalf("directory command: %v", args)
				}
				prepared = true
			} else if args[0] == "-D" && !prepared {
				t.Fatal("binary installed before traversable directory")
			}
		}
		return nil, nil
	}
	if err := m.install(t.Context(), &r); err != nil {
		t.Fatal(err)
	}
	if !prepared {
		t.Fatal("version directory permissions not set")
	}
}
