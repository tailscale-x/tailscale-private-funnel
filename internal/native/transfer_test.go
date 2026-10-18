package native

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestNativeTransferFenceAndRemovalNeverRevokes(t *testing.T) {
	m := manager(t)
	os.MkdirAll(m.UnitDir, 0755)
	r, err := m.Resolve("controls", "main", "control", "Main", "main", "dev-test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		os.MkdirAll(p, 0700)
		os.WriteFile(filepath.Join(p, ".funnel-native-resource"), []byte(r.ID), 0600)
	}
	st, err := store.Open(r.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	st.SetSecret("preserve", "value")
	st.Close()
	os.WriteFile(filepath.Join(m.UnitDir, r.Unit), []byte("[Service]"), 0644)
	m.save(&r)
	calls := []string{}
	m.Run = func(_ context.Context, program string, args []string, _ io.Reader, _ []string) ([]byte, error) {
		calls = append(calls, program+" "+strings.Join(args, " "))
		if args[0] == "show" {
			return []byte("0"), nil
		}
		if args[0] == "is-active" {
			return []byte("inactive"), errors.New("inactive")
		}
		return nil, nil
	}
	m.Cleanup = func(context.Context, Resource) error { t.Fatal("ordinary revocation called"); return nil }
	if err = m.FenceTransfer(t.Context(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Phase != "Transfer fenced" {
		t.Fatal("not fenced")
	}
	for _, action := range []string{"start", "restart", "stop"} {
		if _, err = m.Service(t.Context(), "controls", "main", action); err == nil {
			t.Fatal("fenced lifecycle accepted", action)
		}
	}
	if _, err = m.Update(t.Context(), "controls", "main", "dev-next", ""); err == nil {
		t.Fatal("fenced update accepted")
	}
	if err = m.Delete(t.Context(), "controls", "main"); err == nil {
		t.Fatal("ordinary delete accepted")
	}
	if !strings.Contains(strings.Join(calls, "\n"), "systemctl mask ") {
		t.Fatal("mask missing", calls)
	}
	if err = m.RemoveTransferred(t.Context(), r, "transfer-id"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{r.StateDir, r.DataDir, m.path("controls", "main")} {
		if _, err = os.Stat(p); !os.IsNotExist(err) {
			t.Fatal("owned source state retained", p)
		}
	}
}
func TestNativeTransferOwnershipFailureDoesNotStop(t *testing.T) {
	m := manager(t)
	r, _ := m.Resolve("controls", "main", "control", "", "main", "dev-test", "", "")
	m.Run = func(context.Context, string, []string, io.Reader, []string) ([]byte, error) {
		t.Fatal("called systemd before ownership check")
		return nil, nil
	}
	if err := m.FenceTransfer(t.Context(), &r); err == nil {
		t.Fatal("invalid ownership accepted")
	}
}
