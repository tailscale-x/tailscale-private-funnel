package web

import (
	"connectrpc.com/connect"
	"database/sql"
	"fmt"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Opt-in backend fixture lets existing API/authorization contracts run unchanged.
func testWebStore(t *testing.T) (*store.Store, error) {
	t.Helper()
	raw := os.Getenv("FUNNEL_TEST_RPC_POSTGRES_URL")
	if raw == "" {
		return store.Open(t.TempDir())
	}
	admin, e := sql.Open("pgx", raw)
	if e != nil {
		return nil, e
	}
	name := fmt.Sprintf("funnel_rpc_%d", time.Now().UnixNano())
	if _, e = admin.Exec("CREATE DATABASE " + name); e != nil {
		return nil, e
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE " + name + " WITH (FORCE)"); admin.Close() })
	u, e := url.Parse(raw)
	if e != nil {
		return nil, e
	}
	u.Path = "/" + name
	file := filepath.Join(t.TempDir(), "url")
	if e = os.WriteFile(file, []byte(u.String()), 0600); e != nil {
		return nil, e
	}
	return store.OpenConfigured(t.TempDir(), "", store.DatabaseOptions{Type: "postgres", URLFile: file, TLSMode: "disable"})
}
func TestPostgresHealthMetadata(t *testing.T) {
	if os.Getenv("FUNNEL_TEST_RPC_POSTGRES_URL") == "" {
		t.Skip("PostgreSQL RPC fixture not configured")
	}
	_, client, _ := rpcFixture(t, Principal{NodeID: "admin"})
	r, e := client.Health(t.Context(), connect.NewRequest(&v1.Empty{}))
	if e != nil {
		t.Fatal(e)
	}
	if r.Msg.DatabaseBackend != "postgres" || !r.Msg.DatabaseReady {
		t.Fatal("incorrect database health", r.Msg)
	}
}
