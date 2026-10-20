package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceDiagnosticSocketIsPrivateAndRejectsUnsafeTargets(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	stop, err := serveServiceDiagnostics(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	path := filepath.Join(dir, "diagnostics", "service.sock")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, err)
	}
	if closeAgain, err := serveServiceDiagnostics(ctx, dir, nil); err == nil {
		closeAgain()
		t.Fatal("replaced an active diagnostic socket")
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
	for _, query := range []string{"ip=127.0.0.1&port=80", "ip=100.64.0.1&port=80&action=logout", "ip=100.64.0.1&port=80&port=443", "ip=100.64.0.1&port=0", "ip=100.64.0.1;action=logout&port=80"} {
		res, err := client.Get("http://diagnostic/service?" + query)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatal(query, res.StatusCode)
		}
	}
}
