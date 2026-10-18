package store

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/tailscale-x/tailscale-private-funnel/internal/secretbox"
)

//go:embed postgres/*.sql
var postgresMigrations embed.FS

type DatabaseOptions struct {
	RequireExisting bool   `json:"-"`
	Type            string `json:"type,omitempty"`
	URLFile         string `json:"url_file,omitempty"`
	CAFile          string `json:"ca_file,omitempty"`
	TLSMode         string `json:"tls_mode,omitempty"`
}

func OpenConfigured(dataDir, databaseDir string, o DatabaseOptions) (*Store, error) {
	if o.Type == "" || o.Type == "sqlite" {
		if o.URLFile != "" || o.CAFile != "" || o.TLSMode != "" {
			return nil, errors.New("PostgreSQL settings require database type postgres")
		}
		if databaseDir == "" {
			databaseDir = dataDir
		}
		return OpenDatabase(dataDir, databaseDir)
	}
	if o.Type != "postgres" {
		return nil, errors.New("database type must be sqlite or postgres")
	}
	if o.URLFile == "" {
		return nil, errors.New("PostgreSQL connection file required")
	}
	info, err := os.Stat(o.URLFile)
	if err != nil {
		return nil, errors.New("PostgreSQL connection file unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0007 != 0 {
		return nil, errors.New("PostgreSQL connection file must be protected from other users")
	}
	raw, err := os.ReadFile(o.URLFile)
	if err != nil {
		return nil, errors.New("PostgreSQL connection file unavailable")
	}

	mode := o.TLSMode
	if mode == "" {
		mode = "verify-full"
	}
	if mode != "verify-full" && mode != "verify-ca" && mode != "require" && mode != "disable" {
		return nil, errors.New("unsupported PostgreSQL TLS mode")
	}
	connection := strings.TrimSpace(string(raw))
	// Let pgx compute TLS and multi-host behavior from the final configuration.
	// Explicit TLS options override the URI and ambient PGSSLMODE.
	if strings.HasPrefix(connection, "postgres://") || strings.HasPrefix(connection, "postgresql://") {
		u, e := url.Parse(connection)
		if e != nil {
			return nil, errors.New("invalid PostgreSQL connection configuration")
		}
		q := u.Query()
		q.Set("sslmode", mode)
		if o.CAFile != "" {
			q.Set("sslrootcert", o.CAFile)
		}
		u.RawQuery = q.Encode()
		connection = u.String()
	} else {
		quote := func(value string) string {
			return "'" + strings.ReplaceAll(strings.ReplaceAll(value, "\\", "\\\\"), "'", "\\'") + "'"
		}
		connection += " sslmode=" + quote(mode)
		if o.CAFile != "" {
			connection += " sslrootcert=" + quote(o.CAFile)
		}
	}
	cfg, err := pgx.ParseConfig(connection)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection or TLS configuration")
	}
	cfg.RuntimeParams["application_name"] = "tailscale-private-funnel"
	cfg.RuntimeParams["search_path"] = "public"
	cfg.ConnectTimeout = 5 * time.Second
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("PostgreSQL connection failed; check availability, credentials and TLS")
	}
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		db.Close()
		return nil, err
	}
	// Read the key first, but do not generate a replacement for an initialized DB.
	keyPath := filepath.Join(dataDir, "master.key")
	key, keyErr := os.ReadFile(keyPath)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		db.Close()
		return nil, errors.New("PostgreSQL initialization unavailable")
	}
	defer tx.Rollback()
	fail := func(e error) (*Store, error) { tx.Rollback(); db.Close(); return nil, e }
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(794238410631)`); err != nil {
		return fail(errors.New("PostgreSQL migration lock unavailable"))
	}
	key, keyErr = os.ReadFile(keyPath)
	var identityExists bool
	if err = tx.QueryRowContext(ctx, `SELECT to_regclass('public.funnel_database_identity') IS NOT NULL`).Scan(&identityExists); err != nil {
		return fail(err)
	}
	if identityExists && (keyErr != nil || len(key) != 32) {
		return fail(errors.New("database encryption key missing or invalid; restore matching identity"))
	}
	if !identityExists {
		if o.RequireExisting {
			return fail(errors.New("initialized PostgreSQL database missing; restore matching database and identity"))
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'`).Scan(&count); err != nil {
			return fail(err)
		}
		if count != 0 {
			return fail(errors.New("fresh PostgreSQL installation requires an empty dedicated database"))
		}
		if keyErr != nil && !os.IsNotExist(keyErr) {
			return fail(keyErr)
		}
		if keyErr == nil && len(key) != 32 {
			return fail(errors.New("invalid database encryption key"))
		}
		if keyErr != nil {
			key, err = secretbox.Open(keyPath)
			if err != nil {
				return fail(err)
			}
		}
		if _, err = tx.ExecContext(ctx, `CREATE TABLE funnel_database_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1), key_hash TEXT NOT NULL, storage_marker TEXT NOT NULL DEFAULT '')`); err != nil {
			return fail(err)
		}
		sum := sha256.Sum256(key)
		if _, err = tx.ExecContext(ctx, `INSERT INTO funnel_database_identity(singleton,key_hash) VALUES(1,$1)`, hex.EncodeToString(sum[:])); err != nil {
			return fail(err)
		}
	}
	var expected string
	if err = tx.QueryRowContext(ctx, `SELECT key_hash FROM funnel_database_identity WHERE singleton=1`).Scan(&expected); err != nil {
		return fail(err)
	}
	sum := sha256.Sum256(key)
	if expected != hex.EncodeToString(sum[:]) {
		return fail(errors.New("database encryption key does not match identity"))
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version TEXT PRIMARY KEY,applied_at BIGINT NOT NULL)`); err != nil {
		return fail(err)
	}
	entries, _ := postgresMigrations.ReadDir("postgres")
	for _, entry := range entries {
		var applied int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=$1`, entry.Name()).Scan(&applied); err != nil {
			return fail(err)
		}
		if applied != 0 {
			continue
		}
		migration, e := postgresMigrations.ReadFile("postgres/" + entry.Name())
		if e != nil {
			return fail(e)
		}
		if _, err = tx.ExecContext(ctx, string(migration)); err != nil {
			return fail(fmt.Errorf("PostgreSQL migration %s failed: %w", entry.Name(), err))
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES($1,$2)`, entry.Name(), time.Now().Unix()); err != nil {
			return fail(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	st := &Store{db: &database{DB: db, postgres: true}, key: key}
	if err := st.checkTransferFence(); err != nil {
		db.Close()
		return nil, err
	}
	return st, nil
}

// StorageMarker binds an external database to its Kubernetes identity Secret.
func (s *Store) StorageMarker(value string) (string, error) {
	if !s.db.postgres {
		return "", errors.New("database marker requires PostgreSQL")
	}
	if value != "" {
		_, e := s.db.Exec(`UPDATE funnel_database_identity SET storage_marker=? WHERE singleton=1 AND (storage_marker='' OR storage_marker=?)`, value, value)
		if e != nil {
			return "", e
		}
	}
	var current string
	e := s.db.QueryRow(`SELECT storage_marker FROM funnel_database_identity WHERE singleton=1`).Scan(&current)
	return current, e
}
