package dns

import (
	"encoding/json"
	"github.com/libdns/libdns"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCloudflareProxyUpdatesExactRecordWithoutReplacingDNS(t *testing.T) {
	proxied := false
	patches := 0
	changed := false
	failure := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := any(nil)
		record := map[string]any{"id": "owned", "name": "app.example.com", "type": "A", "content": "203.0.113.10", "proxied": proxied}
		if changed {
			record["content"] = "203.0.113.99"
		}
		switch {
		case r.URL.Path == "/zones":
			if r.Header.Get("Authorization") != "Bearer zone-read" {
				t.Error("wrong zone token")
			}
			result = []any{map[string]string{"id": "zone"}}
		case r.URL.Path == "/zones/zone/dns_records":
			if r.URL.Query().Get("name") != "app.example.com" {
				t.Error(r.URL)
			}
			result = []any{record, map[string]any{"id": "sibling", "name": "app.example.com", "type": "A", "content": "203.0.113.11", "proxied": false}}
		case r.URL.Path == "/zones/zone/dns_records/owned" && r.Method == http.MethodGet:
			result = record
		case r.URL.Path == "/zones/zone/dns_records/owned" && r.Method == http.MethodPatch:
			if failure {
				w.WriteHeader(403)
				w.Write([]byte("private credential diagnostic"))
				return
			}
			if r.Header.Get("Authorization") != "Bearer dns-write" {
				t.Error("wrong DNS token")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 {
				t.Fatal("PATCH must preserve content/name/comments", body)
			}
			proxied = body["proxied"].(bool)
			wantTTL := float64(60)
			if proxied {
				wantTTL = 1
			}
			if body["ttl"] != wantTTL {
				t.Error(body)
			}
			patches++
			record["proxied"] = proxied
			result = record
		default:
			t.Errorf("unexpected mutation %s %s", r.Method, r.URL)
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	defer server.Close()
	p := &cloudflareProvider{apiBase: server.URL, httpClient: server.Client()}
	p.p.APIToken = "zone-read"
	p.p.ZoneToken = "dns-write"
	records := []libdns.Record{libdns.RR{Name: "app.example.com", Type: "A", Data: "203.0.113.10", TTL: time.Minute}}
	for _, enabled := range []bool{true, false} {
		changes, err := p.ProxyChanges(t.Context(), "example.com", records, enabled)
		if err != nil || len(changes) != 1 {
			t.Fatal(changes, err)
		}
		if err = p.ApplyProxyChanges(t.Context(), changes); err != nil {
			t.Fatal(err)
		}
		again, err := p.ProxyChanges(t.Context(), "example.com", records, enabled)
		if err != nil || len(again) != 0 {
			t.Fatal("not idempotent", again, err)
		}
	}
	if patches != 2 {
		t.Fatal(patches)
	}
	changes, err := p.ProxyChanges(t.Context(), "example.com", records, true)
	if err != nil {
		t.Fatal(err)
	}
	changed = true
	if err = p.ApplyProxyChanges(t.Context(), changes); err == nil {
		t.Fatal("changed record mutated")
	}
	if patches != 2 {
		t.Fatal(patches)
	}
	changed = false
	failure = true
	if err = p.ApplyProxyChanges(t.Context(), changes); err == nil || err.Error() != "cloudflare PATCH: HTTP 403" {
		t.Fatal("unsafe provider error", err)
	}
}
