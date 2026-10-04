package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/secretbox"
	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB
	key []byte
}

// Migrations are shipped inside the executable so a standalone binary and its
// container image apply the same storage evolution without external files.
//
//go:embed migrations/*.sql
var migrations embed.FS

type ProviderRecord struct {
	ID           string   `json:"id"`
	Provider     string   `json:"provider"`
	Zone         string   `json:"zone"`
	Enabled      bool     `json:"enabled"`
	AccountID    string   `json:"account_id,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	TestedAt     int64    `json:"tested_at,omitempty"`
}

type NodeRecord struct {
	ID         string `json:"id"`
	Mode       string `json:"mode"`
	Hostname   string `json:"hostname"`
	PublicKey  string `json:"public_key,omitempty"`
	Revoked    bool   `json:"revoked"`
	LastReport int64  `json:"last_report"`
	TailnetID  string `json:"tailnet_id"`
	RevokedAt  int64  `json:"revoked_at,omitempty"`
}

type Enrollment struct {
	ID        string `json:"id"`
	Mode      string `json:"mode"`
	Hostname  string `json:"hostname"`
	Revoked   bool   `json:"revoked"`
	CreatedAt int64  `json:"created_at"`
}

type AuditEvent struct {
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	CreatedAt int64  `json:"created_at"`
}

type ManagedZone struct {
	ProviderID string `json:"provider_id"`
	Zone       string `json:"zone"`
	OwnerID    string `json:"owner_id"`
	Enabled    bool   `json:"enabled"`
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "funnel.db"))
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	s.key, err = secretbox.Open(filepath.Join(dataDir, "master.key"))
	if err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(version TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	for _, entry := range entries {
		var applied int
		if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, entry.Name()).Scan(&applied); err != nil {
			db.Close()
			return nil, err
		}
		if applied != 0 {
			continue
		}
		migration, readErr := migrations.ReadFile("migrations/" + entry.Name())
		if readErr != nil {
			db.Close()
			return nil, readErr
		}
		if _, execErr := db.Exec(string(migration)); execErr != nil {
			db.Close()
			return nil, execErr
		}
		if _, execErr := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, entry.Name(), time.Now().Unix()); execErr != nil {
			db.Close()
			return nil, execErr
		}
	}
	for _, migration := range []string{
		`ALTER TABLE providers ADD COLUMN account_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE providers ADD COLUMN capabilities TEXT NOT NULL DEFAULT '[]'`,
		`ALTER TABLE providers ADD COLUMN tested_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN enrollment_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN revoked_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN tailnet_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE nodes ADD COLUMN addresses TEXT NOT NULL DEFAULT '[]'`,
	} {
		if _, migErr := db.Exec(migration); migErr != nil && !strings.Contains(migErr.Error(), "duplicate column name") {
			db.Close()
			return nil, migErr
		}
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) SetSecret(name, value string) error {
	ciphertext, err := secretbox.Seal(s.key, value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(name,value,secret) VALUES(?,?,1) ON CONFLICT(name) DO UPDATE SET value=excluded.value,secret=1`, name, ciphertext)
	return err
}

func (s *Store) Audit(action, actor string) error {
	_, err := s.db.Exec(`INSERT INTO audit(action,actor,created_at) VALUES(?,?,?)`, action, actor, time.Now().Unix())
	return err
}

func (s *Store) AuditEvents(limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT action,actor,created_at FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.Action, &e.Actor, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) GetSecret(name string) (string, error) {
	var value string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE name=? AND secret=1`, name).Scan(&value); err != nil {
		return "", err
	}
	return secretbox.OpenValue(s.key, value)
}

func (s *Store) SetJSON(name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings(name,value,secret) VALUES(?,?,0) ON CONFLICT(name) DO UPDATE SET value=excluded.value,secret=0`, name, string(b))
	return err
}

func (s *Store) GetJSON(name string, out any) error {
	var value string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE name=? AND secret=0`, name).Scan(&value); err != nil {
		return err
	}
	return json.Unmarshal([]byte(value), out)
}

func (s *Store) SaveReport(nodeID string, payload []byte) error {
	_, err := s.db.Exec(`INSERT INTO reports(node_id,payload,received_at) VALUES(?,?,?) ON CONFLICT(node_id) DO UPDATE SET payload=excluded.payload,received_at=excluded.received_at`, nodeID, string(payload), time.Now().Unix())
	return err
}

func (s *Store) AcceptReport(nodeID, nonce string, payload []byte, publicKey string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seen int
	if err := tx.QueryRow(`SELECT COUNT(1) FROM report_nonces WHERE node_id=? AND nonce=?`, nodeID, nonce).Scan(&seen); err != nil {
		return err
	}
	if seen != 0 {
		return fmt.Errorf("report nonce already used")
	}
	var storedKey string
	var revoked int
	if err := tx.QueryRow(`SELECT node_key,revoked FROM nodes WHERE id=?`, nodeID).Scan(&storedKey, &revoked); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("unknown node")
		}
		return err
	}
	if revoked != 0 {
		return fmt.Errorf("node is revoked")
	}
	if storedKey == "" || storedKey != publicKey {
		return fmt.Errorf("report signing key is not enrolled")
	}
	if _, err := tx.Exec(`INSERT INTO report_nonces(node_id,nonce,received_at) VALUES(?,?,?)`, nodeID, nonce, time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO reports(node_id,payload,received_at) VALUES(?,?,?) ON CONFLICT(node_id) DO UPDATE SET payload=excluded.payload,received_at=excluded.received_at`, nodeID, string(payload), time.Now().Unix()); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE nodes SET last_report=? WHERE id=?`, time.Now().Unix(), nodeID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Reports() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT node_id,payload FROM reports ORDER BY received_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, payload string
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		out[id] = payload
	}
	return out, rows.Err()
}

func (s *Store) StaleReportIDs(before int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT node_id FROM reports WHERE received_at < ?`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// StaleNodes returns enrolled runtime nodes whose last heartbeat is older than
// before. Nodes without a heartbeat are left for explicit administrator
// revocation because they may be completing their first bootstrap.
func (s *Store) StaleNodes(before int64) ([]NodeRecord, error) {
	rows, err := s.db.Query(`SELECT id,mode,hostname,public_key,revoked,last_report,tailnet_id,revoked_at FROM nodes WHERE revoked=0 AND mode IN ('loadbalancer','ingress') AND last_report>0 AND last_report < ?`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeRecord
	for rows.Next() {
		var n NodeRecord
		var revoked int
		if err := rows.Scan(&n.ID, &n.Mode, &n.Hostname, &n.PublicKey, &revoked, &n.LastReport, &n.TailnetID, &n.RevokedAt); err != nil {
			return nil, err
		}
		n.Revoked = revoked != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) DeleteReport(nodeID string) error {
	_, err := s.db.Exec(`DELETE FROM reports WHERE node_id=?`, nodeID)
	return err
}

func (s *Store) SaveProvider(id, provider, zone string, config map[string]string, enabled bool) error {
	b, err := json.Marshal(config)
	if err != nil {
		return err
	}
	sealed, err := secretbox.Seal(s.key, string(b))
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO providers(id,provider,zone,config,enabled,account_id) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider=excluded.provider,zone=excluded.zone,config=excluded.config,enabled=excluded.enabled,account_id=excluded.account_id`, id, provider, zone, sealed, enabled, config["account_id"])
	return err
}

func (s *Store) Providers() ([]ProviderRecord, error) {
	rows, err := s.db.Query(`SELECT id,provider,zone,enabled,account_id,capabilities,tested_at FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderRecord
	for rows.Next() {
		var p ProviderRecord
		var enabled int
		var capabilities string
		if err := rows.Scan(&p.ID, &p.Provider, &p.Zone, &enabled, &p.AccountID, &capabilities, &p.TestedAt); err != nil {
			return nil, err
		}
		p.Enabled = enabled != 0
		_ = json.Unmarshal([]byte(capabilities), &p.Capabilities)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProviderConfig(id string) (ProviderRecord, map[string]string, error) {
	var p ProviderRecord
	var enabled int
	var sealed string
	var capabilities string
	if err := s.db.QueryRow(`SELECT id,provider,zone,config,enabled,account_id,capabilities,tested_at FROM providers WHERE id=?`, id).Scan(&p.ID, &p.Provider, &p.Zone, &sealed, &enabled, &p.AccountID, &capabilities, &p.TestedAt); err != nil {
		return p, nil, err
	}
	p.Enabled = enabled != 0
	_ = json.Unmarshal([]byte(capabilities), &p.Capabilities)
	plain, err := secretbox.OpenValue(s.key, sealed)
	if err != nil {
		return p, nil, err
	}
	var config map[string]string
	if err := json.Unmarshal([]byte(plain), &config); err != nil {
		return p, nil, err
	}
	return p, config, nil
}

func (s *Store) SetProviderEnabled(id string, enabled bool) error {
	_, err := s.db.Exec(`UPDATE providers SET enabled=? WHERE id=?`, enabled, id)
	return err
}

func (s *Store) MarkProviderTested(id string, capabilities []string) error {
	b, _ := json.Marshal(capabilities)
	_, err := s.db.Exec(`UPDATE providers SET tested_at=?,capabilities=? WHERE id=?`, time.Now().Unix(), string(b), id)
	return err
}

func (s *Store) DeleteProvider(id string) error {
	_, err := s.db.Exec(`DELETE FROM providers WHERE id=?`, id)
	return err
}

func (s *Store) SaveZone(providerID, zone, ownerID string, enabled bool) error {
	_, err := s.db.Exec(`INSERT INTO managed_zones(provider_id,zone,owner_id,enabled) VALUES(?,?,?,?) ON CONFLICT(provider_id,zone) DO UPDATE SET owner_id=excluded.owner_id,enabled=excluded.enabled`, providerID, zone, ownerID, enabled)
	return err
}

func (s *Store) Zones() ([]ManagedZone, error) {
	rows, err := s.db.Query(`SELECT provider_id,zone,owner_id,enabled FROM managed_zones ORDER BY provider_id,zone`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedZone
	for rows.Next() {
		var z ManagedZone
		var enabled int
		if err := rows.Scan(&z.ProviderID, &z.Zone, &z.OwnerID, &enabled); err != nil {
			return nil, err
		}
		z.Enabled = enabled != 0
		out = append(out, z)
	}
	return out, rows.Err()
}

func (s *Store) UpsertNode(id, mode, hostname string) error {
	_, err := s.db.Exec(`INSERT INTO nodes(id,mode,hostname) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,hostname=excluded.hostname`, id, mode, hostname)
	return err
}

func (s *Store) UpsertNodeEnrollment(id, enrollmentID, mode, hostname string) error {
	_, err := s.db.Exec(`INSERT INTO nodes(id,mode,hostname,enrollment_id) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,hostname=excluded.hostname,enrollment_id=excluded.enrollment_id`, id, mode, hostname, enrollmentID)
	return err
}

func (s *Store) CreateEnrollment(mode, hostname string) (Enrollment, string, error) {
	codeBytes := make([]byte, 24)
	if _, err := rand.Read(codeBytes); err != nil {
		return Enrollment{}, "", err
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return Enrollment{}, "", err
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	code := base64.RawURLEncoding.EncodeToString(codeBytes)
	hash := sha256.Sum256([]byte(code))
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO enrollments(id,mode,hostname,code_hash,created_at,revoked) VALUES(?,?,?,?,?,0)`, id, mode, hostname, fmt.Sprintf("%x", hash[:]), now)
	return Enrollment{ID: id, Mode: mode, Hostname: hostname, CreatedAt: now}, code, err
}

func (s *Store) RedeemEnrollment(id, code string) (Enrollment, error) {
	var e Enrollment
	var revoked int
	var hash string
	if err := s.db.QueryRow(`SELECT id,mode,hostname,code_hash,created_at,revoked FROM enrollments WHERE id=?`, id).Scan(&e.ID, &e.Mode, &e.Hostname, &hash, &e.CreatedAt, &revoked); err != nil {
		return e, err
	}
	e.Revoked = revoked != 0
	if e.Revoked {
		return e, fmt.Errorf("enrollment is revoked")
	}
	sum := sha256.Sum256([]byte(code))
	if hash != fmt.Sprintf("%x", sum[:]) {
		return e, fmt.Errorf("invalid enrollment code")
	}
	return e, nil
}

// BindEnrollment consumes the code and binds its signing key and verified
// tailnet identity in one transaction. A competing redemption cannot reuse it.
func (s *Store) BindEnrollment(id, code, publicKey, tailnetID string, addresses []string) error {
	if code == "" || publicKey == "" || tailnetID == "" {
		return fmt.Errorf("code, key, and tailnet identity are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sum := sha256.Sum256([]byte(code))
	result, err := tx.Exec(`UPDATE enrollments SET code_hash='' WHERE id=? AND code_hash=? AND revoked=0`, id, fmt.Sprintf("%x", sum[:]))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("invalid, consumed, or revoked enrollment")
	}
	b, _ := json.Marshal(addresses)
	_, err = tx.Exec(`INSERT INTO nodes(id,enrollment_id,mode,hostname,node_key,tailnet_id,addresses) SELECT id,id,mode,hostname,?,?,? FROM enrollments WHERE id=?`, publicKey, tailnetID, string(b), id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Enrollments(mode string) ([]Enrollment, error) {
	rows, err := s.db.Query(`SELECT id,mode,hostname,created_at,revoked FROM enrollments WHERE mode=? ORDER BY created_at DESC`, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Enrollment
	for rows.Next() {
		var e Enrollment
		var revoked int
		if err := rows.Scan(&e.ID, &e.Mode, &e.Hostname, &e.CreatedAt, &revoked); err != nil {
			return nil, err
		}
		e.Revoked = revoked != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) RevokeEnrollment(id string) error {
	_, err := s.db.Exec(`UPDATE enrollments SET revoked=1 WHERE id=?`, id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE nodes SET revoked=1,revoked_at=? WHERE enrollment_id=?`, time.Now().Unix(), id)
	}
	if err == nil {
		_, err = s.db.Exec(`UPDATE relay_assignments SET revoked=1,last_seen=? WHERE node_id IN (SELECT id FROM nodes WHERE enrollment_id=?)`, time.Now().Unix(), id)
	}
	return err
}

func (s *Store) RevokeNode(id string) error {
	_, err := s.db.Exec(`UPDATE nodes SET revoked=1,revoked_at=? WHERE id=?`, time.Now().Unix(), id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE enrollments SET revoked=1 WHERE id=?`, id)
	}
	if err == nil {
		_, err = s.db.Exec(`UPDATE relay_assignments SET revoked=1,last_seen=? WHERE node_id=?`, time.Now().Unix(), id)
	}
	return err
}

func (s *Store) BindNodeKey(id, publicKey string) error {
	_, err := s.db.Exec(`UPDATE nodes SET node_key=? WHERE id=? AND revoked=0`, publicKey, id)
	return err
}

func (s *Store) Node(id string) (NodeRecord, error) {
	var n NodeRecord
	var revoked int
	err := s.db.QueryRow(`SELECT id,mode,hostname,COALESCE(node_key,''),revoked,last_report,revoked_at,tailnet_id FROM nodes WHERE id=?`, id).Scan(&n.ID, &n.Mode, &n.Hostname, &n.PublicKey, &revoked, &n.LastReport, &n.RevokedAt, &n.TailnetID)
	n.Revoked = revoked != 0
	return n, err
}

func (s *Store) NodeByTailnetID(tailnetID string) (NodeRecord, error) {
	var n NodeRecord
	var revoked int
	err := s.db.QueryRow(`SELECT id,mode,hostname,COALESCE(node_key,''),revoked,last_report,revoked_at,tailnet_id FROM nodes WHERE tailnet_id=?`, tailnetID).Scan(&n.ID, &n.Mode, &n.Hostname, &n.PublicKey, &revoked, &n.LastReport, &n.RevokedAt, &n.TailnetID)
	n.Revoked = revoked != 0
	return n, err
}

func (s *Store) NodeRevoked(id string) (bool, error) {
	var revoked int
	err := s.db.QueryRow(`SELECT revoked FROM nodes WHERE id=?`, id).Scan(&revoked)
	if err == sql.ErrNoRows {
		return true, nil
	}
	return revoked != 0, err
}

// TouchNodeReport records liveness for authenticated node health and report
// submissions. It is deliberately separate from the report payload so
// loadbalancers can publish endpoint health without fabricating an exposure
// report.
func (s *Store) TouchNodeReport(id string) error {
	_, err := s.db.Exec(`UPDATE nodes SET last_report=? WHERE id=? AND revoked=0`, time.Now().Unix(), id)
	return err
}

func (s *Store) Nodes(mode string) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT id,mode,hostname,revoked,last_report FROM nodes WHERE mode=? ORDER BY hostname`, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id, m, hostname string
		var revoked, lastReport int64
		if err := rows.Scan(&id, &m, &hostname, &revoked, &lastReport); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "mode": m, "hostname": hostname, "revoked": revoked != 0, "last_report": lastReport})
	}
	return out, rows.Err()
}

func (s *Store) Ledger(providerID, zone, ownerID string) ([]dns.LedgerEntry, error) {
	rows, err := s.db.Query(`SELECT name,type,value FROM records WHERE provider_id=? AND zone=? AND owner_id=?`, providerID, zone, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dns.LedgerEntry
	for rows.Next() {
		var name, typ, value string
		if err := rows.Scan(&name, &typ, &value); err != nil {
			return nil, err
		}
		out = append(out, dns.LedgerEntry{Record: libdns.RR{Name: name, Type: typ, Data: value}, OwnerID: ownerID})
	}
	return out, rows.Err()
}

// ZoneLedger includes every owner, including other provider configurations for
// the same zone. An overwrite permission must never steal another owner's record.
func (s *Store) ZoneLedger(zone string) ([]dns.LedgerEntry, error) {
	rows, err := s.db.Query(`SELECT name,type,value,owner_id FROM records WHERE lower(rtrim(zone,'.'))=lower(rtrim(?,'.'))`, zone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dns.LedgerEntry
	for rows.Next() {
		var name, typ, value, owner string
		if err := rows.Scan(&name, &typ, &value, &owner); err != nil {
			return nil, err
		}
		out = append(out, dns.LedgerEntry{Record: libdns.RR{Name: name, Type: typ, Data: value}, OwnerID: owner})
	}
	return out, rows.Err()
}

func (s *Store) ReplaceLedger(providerID, zone, ownerID string, records []libdns.Record) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM records WHERE provider_id=? AND zone=? AND owner_id=?`, providerID, zone, ownerID); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, record := range records {
		rr := record.RR()
		key := strings.ToLower(strings.TrimSuffix(rr.Name, ".")) + "\x00" + strings.ToUpper(rr.Type) + "\x00" + strings.TrimSuffix(strings.TrimSpace(rr.Data), ".")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		h := sha256.Sum256([]byte(providerID + "\x00" + zone + "\x00" + ownerID + "\x00" + rr.Name + "\x00" + rr.Type + "\x00" + rr.Data))
		if _, err = tx.Exec(`INSERT INTO records(id,provider_id,zone,name,type,value,owner_id) VALUES(?,?,?,?,?,?,?)`, fmt.Sprintf("%x", h[:]), providerID, zone, rr.Name, rr.Type, rr.Data, ownerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// TransferLedgerOwnership removes prior installation ledger rows for records
// that were explicitly authorized for replacement. It runs only after the
// provider mutation succeeds, so a failed update cannot silently change the
// ownership ledger.
func (s *Store) TransferLedgerOwnership(providerID, zone, ownerID string, records []libdns.Record) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range records {
		rr := record.RR()
		if _, err := tx.Exec(`DELETE FROM records WHERE provider_id=? AND zone=? AND name=? AND type=? AND value=? AND owner_id<>?`, providerID, zone, rr.Name, rr.Type, rr.Data, ownerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) TransferZoneLedgerOwnership(zone, ownerID string, records []libdns.Record) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range records {
		rr := record.RR()
		if _, err := tx.Exec(`DELETE FROM records WHERE lower(rtrim(zone,'.'))=lower(rtrim(?,'.')) AND name=? AND type=? AND value=? AND owner_id<>?`, zone, rr.Name, rr.Type, rr.Data, ownerID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

var schema = `
CREATE TABLE IF NOT EXISTS settings(name TEXT PRIMARY KEY, value TEXT NOT NULL, secret INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS nodes(id TEXT PRIMARY KEY, mode TEXT NOT NULL, hostname TEXT NOT NULL, node_key TEXT, revoked INTEGER NOT NULL DEFAULT 0, last_report INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS enrollments(id TEXT PRIMARY KEY, mode TEXT NOT NULL, hostname TEXT NOT NULL, code_hash TEXT NOT NULL, created_at INTEGER NOT NULL, revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS providers(id TEXT PRIMARY KEY, provider TEXT NOT NULL, zone TEXT NOT NULL, config TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS managed_zones(provider_id TEXT NOT NULL, zone TEXT NOT NULL, owner_id TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, PRIMARY KEY(provider_id,zone));
CREATE TABLE IF NOT EXISTS records(id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, zone TEXT NOT NULL, name TEXT NOT NULL, type TEXT NOT NULL, value TEXT NOT NULL, owner_id TEXT NOT NULL, provider_id_remote TEXT);
CREATE TABLE IF NOT EXISTS reports(node_id TEXT PRIMARY KEY, payload TEXT NOT NULL, received_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS report_nonces(node_id TEXT NOT NULL, nonce TEXT NOT NULL, received_at INTEGER NOT NULL, PRIMARY KEY(node_id,nonce));
CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY AUTOINCREMENT, action TEXT NOT NULL, actor TEXT NOT NULL, created_at INTEGER NOT NULL);
`

func (s *Store) String() string { return fmt.Sprintf("store(%p)", s) }

func (s *Store) BindNodeIdentity(id, publicKey, tailnetID string, addresses []string) error {
	if tailnetID == "" {
		return fmt.Errorf("tailnet identity required")
	}
	b, _ := json.Marshal(addresses)
	result, err := s.db.Exec(`UPDATE nodes SET node_key=?,tailnet_id=?,addresses=? WHERE id=? AND revoked=0 AND (tailnet_id='' OR (tailnet_id=? AND node_key=?))`, publicKey, tailnetID, string(b), id, tailnetID, publicKey)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("node identity already bound or revoked")
	}
	return nil
}

type ApplicationRecord struct {
	ID                  string   `json:"id"`
	ServiceName         string   `json:"service_name"`
	ServiceFQDN         string   `json:"service_fqdn"`
	ServicePort         uint16   `json:"service_port"`
	BackendPort         uint16   `json:"backend_port"`
	PublicHostnames     []string `json:"public_hostnames"`
	AllowedIngressNodes []string `json:"allowed_ingress_nodes"`
	PolicyRevision      int64    `json:"policy_revision"`
	Enabled             bool     `json:"enabled"`
}

type ServiceHostRecord struct {
	ServiceName   string `json:"service_name"`
	DeviceID      string `json:"device_id"`
	Approved      bool   `json:"approved"`
	ApprovalState string `json:"approval_state"`
	UpdatedAt     int64  `json:"updated_at"`
}

type RelayAssignment struct {
	ID             string   `json:"id"`
	ApplicationID  string   `json:"application_id"`
	NodeID         string   `json:"node_id"`
	ContainerID    string   `json:"container_id"`
	BackendIP      string   `json:"backend_ip"`
	BackendPort    uint16   `json:"backend_port"`
	Networks       []string `json:"networks"`
	Reachable      bool     `json:"reachable"`
	ConfigRevision int64    `json:"config_revision"`
	LastSeen       int64    `json:"last_seen"`
	Revoked        bool     `json:"revoked"`
}

func (s *Store) UpsertApplication(a ApplicationRecord) error {
	hosts, _ := json.Marshal(a.PublicHostnames)
	nodes, _ := json.Marshal(a.AllowedIngressNodes)
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO applications(id,service_name,service_fqdn,service_port,backend_port,public_hostnames,allowed_ingress_nodes,policy_revision,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET service_name=excluded.service_name,service_fqdn=excluded.service_fqdn,service_port=excluded.service_port,backend_port=excluded.backend_port,public_hostnames=excluded.public_hostnames,allowed_ingress_nodes=excluded.allowed_ingress_nodes,policy_revision=excluded.policy_revision,enabled=excluded.enabled,updated_at=excluded.updated_at`, a.ID, a.ServiceName, a.ServiceFQDN, a.ServicePort, a.BackendPort, string(hosts), string(nodes), a.PolicyRevision, boolInt(a.Enabled), now, now)
	return err
}

func (s *Store) Applications() ([]ApplicationRecord, error) {
	rows, err := s.db.Query(`SELECT id,service_name,service_fqdn,service_port,backend_port,public_hostnames,allowed_ingress_nodes,policy_revision,enabled FROM applications ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApplicationRecord
	for rows.Next() {
		var a ApplicationRecord
		var hosts, nodes string
		var enabled int
		if err := rows.Scan(&a.ID, &a.ServiceName, &a.ServiceFQDN, &a.ServicePort, &a.BackendPort, &hosts, &nodes, &a.PolicyRevision, &enabled); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(hosts), &a.PublicHostnames)
		_ = json.Unmarshal([]byte(nodes), &a.AllowedIngressNodes)
		a.Enabled = enabled != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpsertServiceHost(h ServiceHostRecord) error {
	_, err := s.db.Exec(`INSERT INTO service_hosts(service_name,device_id,approved,approval_state,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(service_name,device_id) DO UPDATE SET approved=excluded.approved,approval_state=excluded.approval_state,updated_at=excluded.updated_at`, h.ServiceName, h.DeviceID, boolInt(h.Approved), h.ApprovalState, time.Now().Unix())
	return err
}

func (s *Store) UpsertRelay(sc RelayAssignment) error {
	networks, _ := json.Marshal(sc.Networks)
	_, err := s.db.Exec(`INSERT INTO relay_assignments(id,application_id,node_id,container_id,backend_ip,backend_port,networks,reachable,config_revision,last_seen,revoked) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET application_id=excluded.application_id,node_id=excluded.node_id,container_id=excluded.container_id,backend_ip=excluded.backend_ip,backend_port=excluded.backend_port,networks=excluded.networks,reachable=excluded.reachable,config_revision=excluded.config_revision,last_seen=excluded.last_seen,revoked=excluded.revoked`, sc.ID, sc.ApplicationID, sc.NodeID, sc.ContainerID, sc.BackendIP, sc.BackendPort, string(networks), boolInt(sc.Reachable), sc.ConfigRevision, sc.LastSeen, boolInt(sc.Revoked))
	return err
}

func (s *Store) ReconcileRelays(nodeID string, active map[string]bool) error {
	rows, err := s.db.Query(`SELECT id FROM relay_assignments WHERE node_id=?`, nodeID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if !active[id] {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := s.db.Exec(`UPDATE relay_assignments SET revoked=1,last_seen=? WHERE id=?`, time.Now().Unix(), id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RevokeRelays(nodeID string) error {
	_, err := s.db.Exec(`UPDATE relay_assignments SET revoked=1,last_seen=? WHERE node_id=?`, time.Now().Unix(), nodeID)
	return err
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
