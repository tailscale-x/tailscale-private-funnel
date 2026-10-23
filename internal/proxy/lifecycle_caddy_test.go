//go:build caddy

package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
)

func TestTemporaryChallengeRouteCaddyLifecycle(t *testing.T) {
	backend := func(body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	}
	harbor, solver, replacement := backend("harbor"), backend("solver-key"), backend("replacement-key")
	defer harbor.Close()
	defer solver.Close()
	defer replacement.Close()
	var solverAddress atomic.Value
	solverAddress.Store(solver.Listener.Addr().String())
	SetDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
		target := harbor.Listener.Addr().String()
		if address == "100.64.0.2:8089" {
			target = solverAddress.Load().(string)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	})
	defer SetDialer(nil)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	catchAll := HTTPRule{Matches: []Match{{Path: "/"}}, Backends: []Backend{{Key: "harbor", Service: "100.64.0.1", Port: 8080, Weight: 1}}}
	challenge := HTTPRule{Matches: []Match{{Path: "/.well-known/acme-challenge/token", Exact: true}}, Backends: []Backend{{Key: "solver", Service: "100.64.0.2", Port: 8089, Weight: 1}}}
	dataDir := t.TempDir()
	autosave := caddy.ConfigAutosavePath
	caddy.ConfigAutosavePath = filepath.Join(dataDir, "autosave.json")
	defer func() { caddy.ConfigAutosavePath = autosave }()
	load := func(rules []HTTPRule) {
		t.Helper()
		text, e := caddyfile(":443", "", dataDir, "", []Route{{Hostname: "harbor.example.test", Rules: rules}})
		if e != nil {
			t.Fatal(e)
		}
		raw, _, e := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(text), nil)
		if e != nil {
			t.Fatal(e)
		}
		var root map[string]any
		if e = json.Unmarshal(raw, &root); e != nil {
			t.Fatal(e)
		}
		httpApp := root["apps"].(map[string]any)["http"].(map[string]any)
		var selected map[string]any
		for _, value := range httpApp["servers"].(map[string]any) {
			server := value.(map[string]any)
			for _, v := range server["listen"].([]any) {
				if v == ":443" {
					selected = server
				}
			}
		}
		if selected == nil {
			t.Fatal("public server missing")
		}
		selected["listen"] = []string{address}
		delete(selected, "tls_connection_policies")
		selected["automatic_https"] = map[string]any{"disable": true}
		root["admin"] = map[string]any{"disabled": true}
		// Retain production's HTTP app settings, especially its drain budget.
		httpApp["servers"] = map[string]any{"test": selected}
		root["apps"] = map[string]any{"http": httpApp, "tls": map[string]any{"disable_storage_clean": true}}
		raw, e = json.Marshal(root)
		if e != nil {
			t.Fatal(e)
		}
		if e = caddy.Load(raw, false); e != nil {
			t.Fatal(e)
		}
	}
	get := func(path string) (string, error) {
		req, e := http.NewRequest("GET", "http://"+address+path, nil)
		if e != nil {
			return "", e
		}
		req.Host = "harbor.example.test"
		r, e := client.Do(req)
		if e != nil {
			return "", e
		}
		defer r.Body.Close()
		b, e := io.ReadAll(r.Body)
		if r.StatusCode != 200 {
			return "", fmt.Errorf("status %d", r.StatusCode)
		}
		return string(b), e
	}
	defer caddy.Stop()
	load([]HTTPRule{catchAll})
	stop := make(chan struct{})
	done := make(chan error, 1)
	var probes atomic.Int32
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			body, e := get("/api/v2.0/health")
			if e != nil || body != "harbor" {
				done <- fmt.Errorf("Harbor disrupted: %q %v", body, e)
				return
			}
			probes.Add(1)
		}
	}()
	for _, phase := range []string{"pending solver", "ready solver", "endpoint gap", "Pod replacement", "route deletion", "namespace deletion"} {
		t.Log(phase)
		rules := []HTTPRule{catchAll}
		want := "harbor"
		if phase == "ready solver" || phase == "endpoint gap" || phase == "Pod replacement" {
			rules = []HTTPRule{challenge, catchAll}
			want = "solver-key"
		}
		if phase == "Pod replacement" {
			solverAddress.Store(replacement.Listener.Addr().String())
			want = "replacement-key"
			// Pod replacement closes only its own backend connections. Do not
			// tear down the shared public probe client's Harbor connections.
			solver.CloseClientConnections()
		}
		load(rules)
		body, e := get("/.well-known/acme-challenge/token")
		if e != nil || body != want {
			close(stop)
			<-done
			t.Fatalf("%s: %q %v", phase, body, e)
		}
	}
	close(stop)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if probes.Load() == 0 {
		t.Fatal("no concurrent Harbor probes")
	}
	t.Logf("%d successful concurrent Harbor requests", probes.Load())
}
