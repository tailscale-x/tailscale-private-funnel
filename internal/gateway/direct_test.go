package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	docker "github.com/docker/docker/client"
)

func TestDockerForwardsDirectlyWithoutDaemonMutations(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for n := 0; n < 256; n++ {
			fmt.Fprintf(w, "data: %d %s\n\n", n, strings.Repeat("x", 128))
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer backend.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	labels := map[string]string{ExposureLabel: fmt.Sprintf("%d:app.example.com:http", port)}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Docker mutation forbidden: %s %s", r.Method, r.URL)
			http.Error(w, "forbidden", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			json.NewEncoder(w).Encode([]types.Container{{ID: "application", State: "running", Labels: labels}})
		case strings.Contains(r.URL.Path, "/containers/"):
			json.NewEncoder(w).Encode(map[string]any{"Id": "application", "State": map[string]any{"Running": true}, "Config": map[string]any{"Labels": labels}, "NetworkSettings": map[string]any{"Networks": map[string]any{"application": map[string]any{"NetworkID": "network", "IPAddress": "127.0.0.1"}}}})
		case strings.Contains(r.URL.Path, "/networks/"):
			json.NewEncoder(w).Encode(map[string]any{"Name": "application", "Id": "network", "IPAM": map[string]any{"Config": []any{map[string]string{"Subnet": "127.0.0.0/8"}}}})
		default:
			t.Errorf("unexpected Docker operation %s", r.URL)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer daemon.Close()
	client, e := docker.NewClientWithOpts(docker.WithHost(daemon.URL), docker.WithVersion("1.47"))
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	conn := &listenerFixtureConnection{}
	listeners := listenerManagerFixture(t, conn)
	config := Config{Docker: client, ListenerManager: listeners, Resolver: StaticResolver{"app.example.com": {ApplicationID: "app", ServiceName: "svc:app", ServicePort: uint16(port), Enabled: true}}}
	exposed, e := config.Reconcile(context.Background())
	if e != nil || len(exposed) != 1 || !exposed[0].Reachable || exposed[0].BackendIP != "127.0.0.1" {
		t.Fatal(exposed, e)
	}
	response, e := http.Get("http://" + conn.listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(response.Body)
	if e != nil || !strings.HasSuffix(string(body), "data: [DONE]\n\n") || strings.Count(string(body), "data:") != 257 {
		t.Fatal("truncated stream", len(body), e)
	}
}
