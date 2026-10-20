package tsconnection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"tailscale.com/client/local"
	"testing"
	"time"
)

func TestRouteLoggingFailureSummaryRecoveryAndRedaction(t *testing.T) {
	var out bytes.Buffer
	o := &routeObserver{logger: slog.New(slog.NewJSONHandler(&out, nil))}
	now := time.Now()
	target := "100.112.216.176:80"
	if o.observe(t.Context(), "tcp", target, time.Second, nil, now) {
		t.Fatal("healthy request logged")
	}
	if !o.observe(t.Context(), "tcp", target, time.Second, ErrNoTailnetRoute, now) {
		t.Fatal("first failure missing")
	}
	for i := 0; i < 100; i++ {
		if o.observe(t.Context(), "tcp", target, time.Second, ErrNoTailnetRoute, now.Add(time.Second)) {
			t.Fatal("repeated failure flooded logs")
		}
	}
	if !o.observe(t.Context(), "tcp", target, time.Second, errors.New("secret-error-value"), now.Add(time.Minute)) {
		t.Fatal("periodic summary missing")
	}
	if !o.observe(t.Context(), "tcp", target, time.Second, nil, now.Add(2*time.Minute)) {
		t.Fatal("recovery missing")
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 || strings.Contains(out.String(), "secret-error-value") {
		t.Fatal(out.String())
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &last); err != nil {
		t.Fatal(err)
	}
	if last["msg"] != "tailnet route dial recovered" || last["failed_attempts"] != float64(102) {
		t.Fatal(last)
	}
	if len(o.failures) != 0 {
		t.Fatal("recovered target retained")
	}
}

func TestRouteLoggingIsBoundedAndIgnoresCanceledRacers(t *testing.T) {
	var out bytes.Buffer
	o := &routeObserver{logger: slog.New(slog.NewJSONHandler(&out, nil))}
	now := time.Now()
	if o.observe(t.Context(), "tcp", "100.64.0.1:80", 0, context.Canceled, now) {
		t.Fatal("canceled racer reported as outage")
	}
	for i := 0; i < 1000; i++ {
		o.observe(t.Context(), "tcp", fmt.Sprintf("100.64.0.1:%d", i+1), 0, ErrNoTailnetRoute, now)
	}
	if len(o.failures) > routeLogTargets {
		t.Fatal("unbounded target retention", len(o.failures))
	}
	if strings.Count(out.String(), "\n") > 4 {
		t.Fatal("global event limit exceeded")
	}
}

func TestRouteLoggingConcurrentDialOutcomes(t *testing.T) {
	var out bytes.Buffer
	o := &routeObserver{logger: slog.New(slog.NewJSONHandler(&out, nil))}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				o.observe(t.Context(), "tcp", "100.64.0.1:80", 0, ErrNoTailnetRoute, time.Now())
				o.observe(t.Context(), "tcp", "100.64.0.1:80", 0, nil, time.Now())
			}
		}()
	}
	wg.Wait()
}

type routeMapTransport func(*http.Request) (*http.Response, error)

func (f routeMapTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPassiveRouteMapSampleDoesNotProbeOrExposeRawMap(t *testing.T) {
	calls := 0
	api := &local.Client{Transport: routeMapTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		body := ""
		switch {
		case r.Method == "GET" && r.URL.Path == "/localapi/v0/status":
			body = `{"Self":{"ID":"edge-device"}}`
		case r.Method == "POST" && r.URL.Path == "/localapi/v0/debug" && r.URL.Query().Get("action") == "current-netmap":
			body = `{"PrivateKey":"never-log-this","Peers":[{"StableID":"service-host","AllowedIPs":["100.64.0.9/32"],"PrimaryRoutes":["100.64.0.9/32"]},{"StableID":"unrelated","Addresses":["100.64.0.10/32"]}]}`
		default:
			t.Fatalf("passive logging made an active or unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	device, peers, err := readServiceMap(t.Context(), api, netip.MustParseAddr("100.64.0.9"))
	if err != nil || calls != 2 || device != "edge-device" || len(peers) != 1 || !peers[0].Primary {
		t.Fatal(device, peers, err, calls)
	}
	b, _ := json.Marshal(peers)
	if strings.Contains(string(b), "never-log-this") || strings.Contains(string(b), "unrelated") {
		t.Fatal("unredacted map metadata", string(b))
	}
}
