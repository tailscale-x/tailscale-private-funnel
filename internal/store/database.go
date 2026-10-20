package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/time/rate"

	"github.com/jmoiron/sqlx"
)

// database keeps dialect concerns out of policy, enrollment and ledger logic.
// One connection preserves the serialized access used by the SQLite backend.
type database struct {
	*sql.DB
	postgres bool
	ctx      context.Context
}
type transaction struct {
	*sql.Tx
	postgres bool
	ctx      context.Context
}

// WithContext bounds connection-pool waits and all nested store queries without
// changing the lifetime or ownership of the shared database.
func (s *Store) WithContext(ctx context.Context) *Store {
	d := *s.db
	d.ctx = ctx
	return &Store{db: &d, key: s.key}
}
func queryContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func bind(q string, postgres bool) string {
	if postgres {
		return sqlx.Rebind(sqlx.DOLLAR, q)
	}
	return q
}
func (d *database) Exec(q string, args ...any) (sql.Result, error) {
	defer d.observeSQL(time.Now(), "exec")
	return d.DB.ExecContext(queryContext(d.ctx), bind(q, d.postgres), arguments(args, d.postgres)...)
}
func (d *database) Query(q string, args ...any) (*sql.Rows, error) {
	defer d.observeSQL(time.Now(), "query")
	return d.DB.QueryContext(queryContext(d.ctx), bind(q, d.postgres), arguments(args, d.postgres)...)
}
func (d *database) QueryRow(q string, args ...any) *sql.Row {
	defer d.observeSQL(time.Now(), "query_row")
	return d.DB.QueryRowContext(queryContext(d.ctx), bind(q, d.postgres), arguments(args, d.postgres)...)
}
func (d *database) Begin() (*transaction, error) {
	defer d.observeSQL(time.Now(), "begin")
	t, e := d.DB.BeginTx(queryContext(d.ctx), nil)
	if e != nil {
		return nil, e
	}
	return &transaction{Tx: t, postgres: d.postgres, ctx: d.ctx}, nil
}
func (t *transaction) Exec(q string, args ...any) (sql.Result, error) {
	return t.Tx.ExecContext(queryContext(t.ctx), bind(q, t.postgres), arguments(args, t.postgres)...)
}
func (t *transaction) Query(q string, args ...any) (*sql.Rows, error) {
	return t.Tx.QueryContext(queryContext(t.ctx), bind(q, t.postgres), arguments(args, t.postgres)...)
}
func (t *transaction) QueryRow(q string, args ...any) *sql.Row {
	return t.Tx.QueryRowContext(queryContext(t.ctx), bind(q, t.postgres), arguments(args, t.postgres)...)
}
func (s *Store) Backend() string {
	if s.db.postgres {
		return "postgres"
	}
	return "sqlite"
}
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func arguments(in []any, postgres bool) []any {
	if !postgres {
		return in
	}
	out := append([]any(nil), in...)
	for i, v := range out {
		if b, ok := v.(bool); ok {
			if b {
				out[i] = int64(1)
			} else {
				out[i] = int64(0)
			}
		}
	}
	return out
}

// Pool wait is included. Never log query text or arguments, which may contain secrets.
var slowSQLLog = rate.NewLimiter(rate.Every(2*time.Second), 4)

func (d *database) observeSQL(start time.Time, operation string) {
	elapsed := time.Since(start)
	if elapsed < time.Second || !slowSQLLog.Allow() {
		return
	}
	_, file, line, _ := runtime.Caller(2)
	stats := d.DB.Stats()
	slog.Warn("slow control storage operation", "operation", operation, "source", filepath.Base(file), "line", line, "elapsed", elapsed, "pool_in_use", stats.InUse, "pool_waits", stats.WaitCount, "pool_wait_total", stats.WaitDuration)
}
