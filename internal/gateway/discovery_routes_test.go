package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
)

type rejectingRouteResolver struct{ specs []Spec }

func (r *rejectingRouteResolver) Resolve(_ context.Context, s Spec) (Assignment, error) {
	r.specs = append(r.specs, s)
	return Assignment{}, errors.New("not approved")
}

func TestDockerDiscoveryDuplicateAndReplacementRoutes(t *testing.T) {
	for _, oldState := range []string{"running", "exited"} {
		t.Run(oldState, func(t *testing.T) {
			labels := map[string]string{"tailscale-private-funnel.routes.api.hostname": "app.example.com", "tailscale-private-funnel.routes.api.port": "8080", "tailscale-private-funnel.routes.api.path-prefix": "/api"}
			items := []types.Container{{ID: "old", State: oldState, Labels: labels}, {ID: "replacement", State: "running", Labels: labels}}
			daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					if r.URL.Query().Get("filters") != "" {
						json.NewEncoder(w).Encode([]types.Container{})
						return
					}
					json.NewEncoder(w).Encode(items)
				case strings.HasSuffix(r.URL.Path, "/json"):
					json.NewEncoder(w).Encode(map[string]any{"Id": "replacement", "NetworkSettings": map[string]any{"Networks": map[string]any{"app-net": map[string]any{"NetworkID": "app-network", "IPAddress": "172.30.0.2"}}}})
				case strings.Contains(r.URL.Path, "/networks/"):
					json.NewEncoder(w).Encode(map[string]any{"Name": "app-net", "Id": "app-network", "IPAM": map[string]any{"Config": []any{map[string]string{"Subnet": "172.30.0.0/24"}}}})
				default:
					t.Errorf("unexpected Docker mutation: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer daemon.Close()
			docker, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(daemon.URL), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer docker.Close()
			resolver := &rejectingRouteResolver{}
			c := Config{Docker: docker, Resolver: resolver, ListenerManager: NewListenerManager(nil)}
			if _, err := c.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			want := 0
			if oldState == "exited" {
				want = 1
			}
			if len(resolver.specs) != want {
				t.Fatalf("resolution calls=%d want %d", len(resolver.specs), want)
			}
			if want == 1 && resolver.specs[0].PathPrefix != "/api" {
				t.Fatal("path lost at discovery")
			}
		})
	}
}

// The opt-in fixture script owns these containers and removes them on exit.
func TestLiveDockerPathDeclarations(t *testing.T) {
	fixture := os.Getenv("FUNNEL_PATH_DOCKER_FIXTURE")
	if fixture == "" {
		t.Skip("run scripts/test-path-routing.sh for live Docker metadata")
	}
	docker, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer docker.Close()
	items, err := docker.ContainerList(t.Context(), container.ListOptions{Filters: filters.NewArgs(filters.Arg("label", "funnel.path-qa="+fixture))})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatal("fixture must contain exactly two containers", len(items))
	}
	paths := map[string]bool{}
	for _, item := range items {
		specs, err := ParseLabels(item.Labels, nil)
		if err != nil || len(specs) != 1 {
			t.Fatal(specs, err)
		}
		spec := specs[0]
		paths[spec.PathPrefix] = true
		if spec.Hostname != "app.example.test" || spec.Port != 8080 || spec.Rewrite == nil {
			t.Fatal(spec)
		}
	}
	if !paths["/api"] || !paths["/api/admin"] {
		t.Fatal(paths)
	}
}
