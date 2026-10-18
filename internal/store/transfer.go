package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// TransferSnapshot is logical application state, never source SQL or migration
// history. Text includes encrypted ciphertext unchanged; integers stay exact.
type TransferSnapshot struct {
	Version int             `json:"version"`
	KeyHash string          `json:"key_hash"`
	Tables  []TransferTable `json:"tables"`
}
type TransferTable struct {
	Name    string            `json:"name"`
	Columns []string          `json:"columns"`
	Rows    [][]TransferValue `json:"rows"`
}
type TransferValue struct {
	Text    *string `json:"text,omitempty"`
	Integer *int64  `json:"integer,omitempty"`
}

var transferTables = []string{"applications", "audit", "backend_assignments", "enrollments", "exposures", "managed_zones", "nodes", "kubernetes_forwarders", "providers", "records", "report_nonces", "reports", "service_hosts", "settings"}

const transferFence = "control_transfer_source"

func (s *Store) checkTransferFence() error {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE name=?`, transferFence).Scan(&v)
	if err == nil {
		return errors.New("control has been transferred; source database cannot be started")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}
func (s *Store) MarkTransferred(id string) error {
	if id == "" {
		return errors.New("transfer ID required")
	}
	_, err := s.db.Exec(`INSERT INTO settings(name,value,secret) VALUES(?,?,0)`, transferFence, id)
	return err
}
func quoteIdentifier(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func (s *Store) transferSchema(ctx context.Context) ([]TransferTable, error) {
	// Refuse exporting when a future schema adds a table we do not understand.
	query := `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`
	if s.db.postgres {
		query = `SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'`
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{"schema_migrations": true, "funnel_database_identity": true}
	for _, t := range transferTables {
		allowed[t] = true
	}
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		if !allowed[n] {
			rows.Close()
			return nil, fmt.Errorf("unsupported transfer table %s", n)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	tables := []TransferTable{}
	for _, name := range transferTables {
		r, e := s.db.QueryContext(ctx, `SELECT * FROM `+quoteIdentifier(name)+` WHERE 1=0`)
		if e != nil {
			return nil, e
		}
		columns, e := r.Columns()
		r.Close()
		if e != nil {
			return nil, e
		}
		sort.Strings(columns)
		tables = append(tables, TransferTable{Name: name, Columns: columns})
	}
	return tables, nil
}
func (s *Store) ExportTransfer(ctx context.Context) (*TransferSnapshot, error) {
	schema, err := s.transferSchema(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	sum := sha256.Sum256(s.key)
	out := &TransferSnapshot{Version: 1, KeyHash: hex.EncodeToString(sum[:]), Tables: schema}
	for i := range out.Tables {
		t := &out.Tables[i]
		cols := []string{}
		for _, c := range t.Columns {
			cols = append(cols, quoteIdentifier(c))
		}
		rows, e := tx.QueryContext(ctx, `SELECT `+strings.Join(cols, ",")+` FROM `+quoteIdentifier(t.Name)+` ORDER BY `+strings.Join(cols, ","))
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			values := make([]any, len(t.Columns))
			ptr := make([]any, len(values))
			for j := range values {
				ptr[j] = &values[j]
			}
			if e = rows.Scan(ptr...); e != nil {
				rows.Close()
				return nil, e
			}
			row := make([]TransferValue, len(values))
			for j, v := range values {
				switch v := v.(type) {
				case nil:
				case int64:
					n := v
					row[j].Integer = &n
				case string:
					x := v
					row[j].Text = &x
				case []byte:
					x := string(v)
					row[j].Text = &x
				default:
					rows.Close()
					return nil, errors.New("unsupported transfer value")
				}
			}
			t.Rows = append(t.Rows, row)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Store) ImportTransfer(ctx context.Context, in *TransferSnapshot) error {
	sum := sha256.Sum256(s.key)
	if in == nil || in.Version != 1 || in.KeyHash != hex.EncodeToString(sum[:]) {
		return errors.New("transfer format or encryption key mismatch")
	}
	schema, err := s.transferSchema(ctx)
	if err != nil {
		return err
	}
	if len(schema) != len(in.Tables) {
		return errors.New("transfer schema mismatch")
	}
	for i, t := range in.Tables {
		a, _ := json.Marshal(t.Columns)
		b, _ := json.Marshal(schema[i].Columns)
		if t.Name != schema[i].Name || string(a) != string(b) {
			return errors.New("transfer schema mismatch")
		}
		for _, r := range t.Rows {
			if len(r) != len(t.Columns) {
				return errors.New("invalid transfer row")
			}
			for _, v := range r {
				if v.Text != nil && v.Integer != nil {
					return errors.New("invalid transfer value")
				}
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if s.db.postgres {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(794238410631)`); err != nil {
			return err
		}
	}
	// Check every table before any insertion; a failed import is all-or-nothing.
	for _, t := range schema {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM `+quoteIdentifier(t.Name)).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("restore requires an empty destination database")
		}
	}
	for _, t := range in.Tables {
		cols := []string{}
		marks := []string{}
		for _, c := range t.Columns {
			cols = append(cols, quoteIdentifier(c))
			marks = append(marks, "?")
		}
		q := bind(`INSERT INTO `+quoteIdentifier(t.Name)+` (`+strings.Join(cols, ",")+`) VALUES (`+strings.Join(marks, ",")+`)`, s.db.postgres)
		for _, r := range t.Rows {
			args := make([]any, len(r))
			for i, v := range r {
				if v.Text != nil {
					args[i] = *v.Text
				}
				if v.Integer != nil {
					args[i] = *v.Integer
				}
			}
			if _, err = tx.ExecContext(ctx, q, args...); err != nil {
				return errors.New("restore import failed; transaction rolled back")
			}
		}
	}
	if s.db.postgres {
		if _, err = tx.ExecContext(ctx, `SELECT setval(pg_get_serial_sequence('audit','id'), GREATEST(COALESCE((SELECT max(id) FROM audit),0),1), EXISTS(SELECT 1 FROM audit))`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
