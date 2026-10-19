package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libdns/libdns"
)

func contractStore(t *testing.T, postgres bool) (*Store, DatabaseOptions, string) {
	t.Helper()
	dir := t.TempDir()
	if !postgres {
		s, e := Open(dir)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { s.Close() })
		return s, DatabaseOptions{}, dir
	}

	o, dir := postgresContractOptions(t)
	s, e := OpenConfigured(dir, "", o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, o, dir
}
func postgresContractOptions(t *testing.T) (DatabaseOptions, string) {
	t.Helper()
	dir := t.TempDir()
	raw := os.Getenv("FUNNEL_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("set FUNNEL_TEST_POSTGRES_URL for PostgreSQL contracts")
	}
	admin, e := sql.Open("pgx", raw)
	if e != nil {
		t.Fatal(e)
	}
	name := fmt.Sprintf("funnel_test_%d", time.Now().UnixNano())
	role := name + "_app"
	if _, e = admin.Exec("CREATE ROLE " + role + " LOGIN PASSWORD 'contract-password'"); e != nil {
		t.Fatal(e)
	}
	if _, e = admin.Exec("CREATE DATABASE " + name + " OWNER " + role); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		admin.Exec("DROP ROLE " + role)
		admin.Close()
	})
	u, e := url.Parse(raw)
	if e != nil {
		t.Fatal(e)
	}
	u.Path = "/" + name
	u.User = url.UserPassword(role, "contract-password")
	file := filepath.Join(t.TempDir(), "database.url")
	os.WriteFile(file, []byte(u.String()), 0600)
	o := DatabaseOptions{Type: "postgres", URLFile: file, TLSMode: "disable"}

	return o, dir
}

func TestDatabaseContract(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "sqlite"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			if e := s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "encrypted-only", "account_id": "account"}, true, "Display"); e != nil {
				t.Fatal(e)
			}
			_, cfg, e := s.ProviderConfig("provider")
			if e != nil || cfg["api_token"] != "encrypted-only" {
				t.Fatal(e, cfg)
			}
			var ciphertext string
			if e = s.db.QueryRow(`SELECT config FROM providers WHERE id=?`, "provider").Scan(&ciphertext); e != nil || strings.Contains(ciphertext, "encrypted-only") {
				t.Fatal("credentials not encrypted", e)
			}
			if e = s.AddZone("provider", "example.com"); e != nil {
				t.Fatal(e)
			}
			records := []libdns.Record{libdns.RR{Name: "app", Type: "CNAME", Data: "edge.example.com.", TTL: time.Minute}}
			if e = s.ReplaceLedger("provider", "example.com", "owned", records); e != nil {
				t.Fatal(e)
			}
			if e = s.ReplaceLedger("provider", "example.com", "other", []libdns.Record{libdns.RR{Name: "keep", Type: "A", Data: "192.0.2.1"}}); e != nil {
				t.Fatal(e)
			}

			// Inject a database write failure after the first replacement row succeeds.
			var trigger string
			if pg {
				trigger = `CREATE FUNCTION reject_ledger_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected ledger failure'; END $$; CREATE TRIGGER reject_ledger_write BEFORE INSERT ON records FOR EACH ROW WHEN (NEW.name='rejected') EXECUTE FUNCTION reject_ledger_write();`
			} else {
				trigger = `CREATE TRIGGER reject_ledger_write BEFORE INSERT ON records WHEN NEW.name='rejected' BEGIN SELECT RAISE(ABORT,'injected ledger failure'); END;`
			}
			if _, e = s.db.Exec(trigger); e != nil {
				t.Fatal(e)
			}
			replacement := append([]libdns.Record{}, records...)
			replacement = append(replacement, libdns.RR{Name: "rejected", Type: "A", Data: "192.0.2.2"})
			if e = s.ReplaceLedger("provider", "example.com", "owned", replacement); e == nil {
				t.Fatal("injected failure committed")
			}

			original, err := s.Ledger("provider", "example.com", "owned")
			if err != nil || len(original) != 1 {
				t.Fatal("failed transaction changed ledger", err)
			}
			if e = s.ReplaceLedger("provider", "example.com", "owned", nil); e != nil {
				t.Fatal(e)
			}
			kept, e := s.Ledger("provider", "example.com", "other")
			if e != nil || len(kept) != 1 {
				t.Fatal("ownership cleanup", kept, e)
			}
			enrollment, code, e := s.CreateEnrollment("docker-gateway", "gateway")
			if e != nil {
				t.Fatal(e)
			}
			var successes atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if s.BindEnrollment(enrollment.ID, code, "key", "device", "gateway", []string{"100.64.0.1"}) == nil {
						successes.Add(1)
					}
				}()
			}
			wg.Wait()
			if successes.Load() != 1 {
				t.Fatal("one-use binding", successes.Load())
			}
			if e = s.AcceptReport(enrollment.ID, "nonce", []byte(`{"test":true}`), "key"); e != nil {
				t.Fatal(e)
			}
			if e = s.AcceptReport(enrollment.ID, "nonce", []byte(`{}`), "key"); e == nil {
				t.Fatal("replay accepted")
			}
			if e = s.AcceptReport("unknown", "x", []byte(`{}`), "key"); e == nil {
				t.Fatal("unknown report accepted")
			}
			if e = s.RevokeNode(enrollment.ID); e != nil {
				t.Fatal(e)
			}
			if e = s.AcceptReport(enrollment.ID, "new", []byte(`{}`), "key"); e == nil {
				t.Fatal("revoked report accepted")
			}
			if e = s.SetJSON("policy", map[string]any{"revision": 2}); e != nil {
				t.Fatal(e)
			}
			var policy map[string]any
			if e = s.GetJSON("policy", &policy); e != nil || policy["revision"] != float64(2) {
				t.Fatal(e)
			}
			if e = s.Audit("contract", "test"); e != nil {
				t.Fatal(e)
			}
			events, e := s.AuditEvents(1)
			if e != nil || len(events) != 1 {
				t.Fatal(e)
			}
			if e = s.UpsertApplication(ApplicationRecord{ID: "app", Name: "App", ServiceName: "svc:app", ServiceFQDN: "app.test.ts.net", ServicePort: 80, BackendPort: 8080, PublicHostnames: []string{"test.example.com"}, Enabled: true}); e != nil {
				t.Fatal(e)
			}
			apps, e := s.Applications()
			if e != nil || len(apps) != 1 {
				t.Fatal(apps, e)
			}
			if e = s.Ping(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestPostgresIdentityAndMigrations(t *testing.T) {
	s, o, dir := contractStore(t, true)
	_, key, e := s.ConfigurationKey()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetSecret("fixture", "private"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StorageMarker("test-marker"); e != nil {
		t.Fatal(e)
	}
	// A second open runs idempotent migrations, preserving keys and ciphertext.
	second, e := OpenConfigured(dir, "", o)
	if e != nil {
		t.Fatal(e)
	}
	value, e := second.GetSecret("fixture")
	if e != nil || value != "private" {
		t.Fatal(e)
	}
	_, key2, e := second.ConfigurationKey()
	if e != nil || string(key) != string(key2) {
		t.Fatal("signing key changed", e)
	}
	second.Close()
	for _, bad := range [][]byte{nil, []byte("corrupt"), make([]byte, 32)} {
		path := filepath.Join(t.TempDir(), "identity")
		os.MkdirAll(path, 0700)
		if bad != nil {
			os.WriteFile(filepath.Join(path, "master.key"), bad, 0600)
		}
		if reopened, e := OpenConfigured(path, "", o); e == nil {
			reopened.Close()
			t.Fatal("wrong/missing key accepted")
		}
	}
	var wg sync.WaitGroup
	errors := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, e := OpenConfigured(dir, "", o)
			if e == nil {
				other.Close()
			}
			errors <- e
		}()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	// Rotation requires a fresh connection; existing data and encryption identity remain.
	raw, _ := os.ReadFile(o.URLFile)
	u, _ := url.Parse(string(raw))
	admin, _ := sql.Open("pgx", os.Getenv("FUNNEL_TEST_POSTGRES_URL"))
	defer admin.Close()
	role := u.User.Username()
	if _, e = admin.Exec("ALTER ROLE " + role + " PASSWORD 'rotated-contract-password'"); e != nil {
		t.Fatal(e)
	}
	u.User = url.UserPassword(role, "rotated-contract-password")
	os.WriteFile(o.URLFile, []byte(u.String()), 0600)
	rotated, e := OpenConfigured(dir, "", o)
	if e != nil {
		t.Fatal(e)
	}
	if v, e := rotated.GetSecret("fixture"); e != nil || v != "private" {
		t.Fatal("rotation lost data", e)
	}
	rotated.Close()
	if s.Backend() != "postgres" {
		t.Fatal("invalid backend")
	}
}
func TestPostgresConfigurationFailuresRedactCredentials(t *testing.T) {
	file := filepath.Join(t.TempDir(), "url")
	os.WriteFile(file, []byte("postgres://user:super-secret@127.0.0.1:1/app"), 0600)
	for _, o := range []DatabaseOptions{{Type: "postgres", URLFile: file}, {Type: "postgres", URLFile: file, TLSMode: "invalid"}, {Type: "sqlite", URLFile: file}, {Type: "unexpected"}} {
		s, e := OpenConfigured(t.TempDir(), "", o)
		if e == nil {
			s.Close()
			t.Fatal("invalid configuration accepted")
		}
		if strings.Contains(e.Error(), "super-secret") {
			t.Fatal("secret exposed")
		}
	}
	os.Chmod(file, 0644)
	if s, e := OpenConfigured(t.TempDir(), "", DatabaseOptions{Type: "postgres", URLFile: file}); e == nil {
		s.Close()
		t.Fatal("unprotected file accepted")
	}
}

func TestPostgresDoesNotDowngradeTLS(t *testing.T) {
	s, o, dir := contractStore(t, true)
	_ = s
	o.TLSMode = "verify-full"
	// Plain disposable PostgreSQL fixtures do not offer trusted TLS. An explicit
	// URI sslmode=disable must not override the application's verify-full default.
	raw, _ := os.ReadFile(o.URLFile)
	u, _ := url.Parse(string(raw))
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	os.WriteFile(o.URLFile, []byte(u.String()), 0600)
	if other, e := OpenConfigured(dir, "", o); e == nil {
		other.Close()
		t.Fatal("connection silently downgraded TLS")
	}
}

func TestPostgresConcurrentFirstInitialization(t *testing.T) {
	o, dir := postgresContractOptions(t)
	errors := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := OpenConfigured(dir, "", o)
			if e == nil {
				s.Close()
			}
			errors <- e
		}()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	s, e := OpenConfigured(dir, "", o)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var count int
	if e = s.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); e != nil || count != 2 {
		t.Fatal("migration applied incorrectly", count, e)
	}
	key, e := os.ReadFile(filepath.Join(dir, "master.key"))
	if e != nil || len(key) != 32 {
		t.Fatal("invalid identity root", e)
	}
}
