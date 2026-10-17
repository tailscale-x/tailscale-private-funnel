package tailscale

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCachedOAuthCoalescesRefreshAndRotatedCredentialsUseNewSource(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture", "expires_in": 3600})
	}))
	defer server.Close()
	client := (Client{HTTP: server.Client(), ClientID: "id", ClientSecret: "secret", OAuthBase: server.URL}).CachedOAuth()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, e := client.oauthToken(t.Context())
			if e != nil || token != "fixture" {
				t.Error(token, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("OAuth token fetched per API call", calls.Load())
	}
	rotated := (Client{HTTP: server.Client(), ClientID: "id", ClientSecret: "rotated", OAuthBase: server.URL}).CachedOAuth()
	if _, e := rotated.oauthToken(t.Context()); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 2 {
		t.Fatal("rotated credentials reused prior source")
	}
	if (Client{}).oauthBase() != "https://api.tailscale.com" {
		t.Fatal("noncanonical default OAuth endpoint")
	}
}
