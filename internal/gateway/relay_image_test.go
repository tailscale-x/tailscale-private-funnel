package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

func TestRelayImageProvisioning(t *testing.T) {
	for _, tc := range []struct {
		name              string
		inspect           int
		stream            string
		wantPull, wantErr bool
	}{
		{"cached", 200, "", false, false},
		{"missing", 404, "{\"status\":\"Downloaded\"}\n", true, false},
		{"registry failure", 404, "{\"error\":\"access denied\"}\n", true, true},
		{"daemon denied", 403, "", false, true},
		{"broken pull", 404, "{", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pulled := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					w.WriteHeader(tc.inspect)
					if tc.inspect == 200 {
						fmt.Fprint(w, `{"Id":"sha256:cached"}`)
					} else {
						fmt.Fprint(w, `{"message":"image unavailable"}`)
					}
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/v1.47/images/create" || r.URL.Query().Get("fromImage") != "ghcr.io/tailscale-x/tailscale-private-funnel" || r.URL.Query().Get("tag") != "dev-test" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				pulled = true
				fmt.Fprint(w, tc.stream)
			}))
			defer server.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			err = (Config{Docker: cli, RelayImage: "ghcr.io/tailscale-x/tailscale-private-funnel:dev-test"}).ensureRelayImage(t.Context())
			if (err != nil) != tc.wantErr || pulled != tc.wantPull {
				t.Fatalf("error=%v pulled=%v", err, pulled)
			}
		})
	}
}
