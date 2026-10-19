package dns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/libdns/libdns"
)

type cloudflareRequestClient func(*http.Request) (*http.Response, error)

func (f cloudflareRequestClient) Do(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the pinned libdns provider's real HTTP serialization: a successful
// API response which qualifies the name must not hide a relative request name.
func TestCloudflareAppendUsesCompleteOwnerName(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"app", "app.example.com"},
		{"api.app", "api.app.example.com"},
		{"app.example.com", "app.example.com"},
		{"api.app.example.com.", "api.app.example.com"},
		{"example.com", "example.com"},
		{"@", "example.com"},
		{"", "example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &cloudflareProvider{}
			creates := 0
			p.p.HTTPClient = cloudflareRequestClient(func(r *http.Request) (*http.Response, error) {
				result := any([]map[string]string{{"id": "zone-id", "name": "example.com"}})
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/client/v4/zones":
					if r.URL.Query().Get("name") != "example.com" {
						t.Fatalf("unexpected zone lookup: %s", r.URL)
					}
				case r.Method == http.MethodPost && r.URL.Path == "/client/v4/zones/zone-id/dns_records":
					creates++
					var body struct {
						Name, Type, Content string
						TTL                 int
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body.Name != tc.want || body.Type != "A" || body.Content != "192.0.2.8" || body.TTL != 60 {
						t.Errorf("Cloudflare request = %+v; want complete name %q and unchanged record data", body, tc.want)
					}
					result = map[string]any{"id": "record-id", "name": tc.want, "type": "A", "content": "192.0.2.8", "ttl": 60}
				default:
					t.Fatalf("unexpected Cloudflare request: %s %s", r.Method, r.URL)
				}
				b, _ := json.Marshal(map[string]any{"success": true, "result": result})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
			})
			err := p.Apply(context.Background(), "example.com", []libdns.Record{libdns.RR{Name: tc.name, Type: "A", Data: "192.0.2.8", TTL: time.Minute}}, nil)
			if err != nil || creates != 1 {
				t.Fatalf("apply: creates=%d err=%v", creates, err)
			}
		})
	}
}

func TestCloudflareCompleteNamesPreserveRecordData(t *testing.T) {
	for _, rr := range []libdns.RR{
		{Name: "nested.app", Type: "A", Data: "192.0.2.8", TTL: time.Minute},
		{Name: "nested.app", Type: "CNAME", Data: "edge.other.example.", TTL: time.Minute},
		{Name: "_gateway._tcp.app", Type: "SRV", Data: "1 2 8080 backend.other.example.", TTL: time.Minute},
		{Name: "_check.app", Type: "TXT", Data: "arbitrary record data", TTL: time.Minute},
	} {
		t.Run(rr.Type, func(t *testing.T) {
			got, err := cloudflareRecords([]libdns.Record{rr}, "example.com.")
			if err != nil {
				t.Fatal(err)
			}
			want := rr
			want.Name += ".example.com"
			if actual := got[0].RR(); actual != want {
				t.Fatalf("got %+v, want %+v", actual, want)
			}
		})
	}
}
