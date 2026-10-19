package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	dockerclient "github.com/docker/docker/client"
)

func TestCompleteDockerInventoryIndependentOfBackends(t *testing.T) {
	items := []types.Container{{ID: "stopped", State: "exited", Labels: map[string]string{ExposureLabel: "8080:app.example.com:http", "secret": "must-not-leave-host"}}, {ID: "replacement", State: "running", Labels: map[string]string{"custom.expose": "8080:other.example.com:http"}}}
	bad := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/containers/json") {
			t.Errorf("inventory touched backend/provisioning: %s", r.URL)
			http.Error(w, "unexpected", 500)
			return
		}
		if r.URL.Query().Get("all") != "1" {
			t.Error("stopped containers excluded")
		}
		if bad {
			http.Error(w, "Docker unavailable", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer api.Close()
	client, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(api.URL), dockerclient.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	c := Config{Docker: client, LabelKeys: []string{"custom.expose"}}
	inv, err := c.Inventory(t.Context())
	if err != nil || !inv.Complete || len(inv.Containers) != 2 || inv.Containers[0].Running {
		t.Fatal(inv, err)
	}
	if _, ok := inv.Containers[0].Labels["secret"]; ok {
		t.Fatal("unrelated labels leaked")
	}
	items[0].Labels[ExposureLabel] = "invalid"
	if _, err = c.Inventory(t.Context()); err == nil {
		t.Fatal("invalid declarations treated as complete")
	}
	bad = true
	if _, err = c.Inventory(t.Context()); err == nil {
		t.Fatal("failed scan treated as complete")
	}
	dir := t.TempDir()
	for want := int64(1); want < 4; want++ {
		got, err := NextInventoryRevision(dir)
		if err != nil || got != want {
			t.Fatal(got, err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "docker-inventory-revision"), []byte("corrupt"), 0600)
	if _, err = NextInventoryRevision(dir); err == nil {
		t.Fatal("corrupt revision reset silently")
	}
}

// Read-only preflight against the actual daemon; never creates containers or
// sends inventory to control. Enable before rolling out automatic retirement.
func TestLiveDockerInventoryPreflight(t *testing.T) {
	if os.Getenv("FUNNEL_DOCKER_INVENTORY_CHECK") != "1" {
		t.Skip("opt-in read-only daemon preflight")
	}
	client, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	inv, err := (Config{Docker: client}).Inventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Complete {
		t.Fatal("inventory is not authoritative")
	}
	t.Logf("complete read-only inventory: %d containers", len(inv.Containers))
}
