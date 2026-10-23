package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
)

var ErrInstanceConflict = errors.New("component assignment lease is held by another instance")

type Registration struct {
	Runtime      string   `json:"runtime,omitempty"`
	ComponentID  string   `json:"component_id"`
	InstanceID   string   `json:"instance_id"`
	DeviceID     string   `json:"device_id"`
	Installation string   `json:"installation"`
	Mode         string   `json:"mode"`
	Name         string   `json:"name"`
	Hostname     string   `json:"hostname"`
	ParentID     string   `json:"parent_id,omitempty"`
	Connection   string   `json:"connection"`
	Addresses    []string `json:"addresses"`
}
type Instance struct {
	ComponentID string `json:"component_id"`
	InstanceID  string `json:"instance_id"`
	DeviceID    string `json:"device_id"`
	Generation  int64  `json:"generation"`
	LeaseUntil  int64  `json:"lease_until"`
}

func (r Registration) Role() identity.Role {
	if r.Mode == "k8s-forwarder" {
		return identity.KubernetesGateway
	}
	return identity.Role(r.Mode)
}

// RegisterComponent is an assignment transaction, never an approval ceremony.
// The caller must authorize verified WhoIs tags before calling it.
func (s *Store) RegisterComponent(r Registration, now time.Time) (Instance, error) {
	out := Instance{ComponentID: r.ComponentID, InstanceID: r.InstanceID, DeviceID: r.DeviceID, Generation: 1, LeaseUntil: now.Add(time.Minute).Unix()}
	if r.ComponentID == "" || r.InstanceID == "" || r.DeviceID == "" || len(r.ComponentID) > 128 || len(r.InstanceID) > 128 || len(r.Name) > 512 || len(r.Hostname) > 255 {
		return out, errors.New("component, instance, device and bounded settings required")
	}
	if e := identity.ValidateInstallation(r.Installation); e != nil {
		return out, e
	}
	if !identity.ValidRole(r.Role()) || r.Mode == "control" {
		return out, errors.New("valid component role required")
	}
	name, err := ValidateName(r.Name)
	if err != nil {
		return out, err
	}
	r.Name = name
	if r.Mode != "k8s-forwarder" && r.ParentID != "" {
		return out, errors.New("only forwarders have a parent")
	}
	if r.Connection != "embedded" && r.Connection != "host" {
		return out, errors.New("connection must be embedded or host")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if r.ParentID != "" {
		var mode string
		var disabled int
		if e = tx.QueryRow(`SELECT mode,revoked FROM nodes WHERE id=?`, r.ParentID).Scan(&mode, &disabled); e != nil || mode != "k8s-gateway" || disabled != 0 {
			return out, errors.New("active logical Kubernetes gateway required")
		}
	}
	var revoked int
	var nodeMode string
	err = tx.QueryRow(`SELECT mode,revoked FROM nodes WHERE id=?`, r.ComponentID).Scan(&nodeMode, &revoked)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if err == nil && nodeMode != r.Mode {
		return out, errors.New("component role cannot change")
	}
	if revoked != 0 {
		return out, errors.New("component disabled; role authority is unchanged")
	}
	var old Instance
	e = tx.QueryRow(`SELECT component_id,instance_id,device_id,generation,lease_until FROM component_instances WHERE component_id=?`, r.ComponentID).Scan(&old.ComponentID, &old.InstanceID, &old.DeviceID, &old.Generation, &old.LeaseUntil)
	if e != nil && e != sql.ErrNoRows {
		return out, e
	}
	if e == nil {
		if old.LeaseUntil > now.Unix() && (old.InstanceID != r.InstanceID || old.DeviceID != r.DeviceID) {
			return out, ErrInstanceConflict
		}
		out.Generation = old.Generation
		if old.InstanceID != r.InstanceID || old.DeviceID != r.DeviceID {
			out.Generation++
		}
	}
	var existing Registration
	var raw string
	e = tx.QueryRow(`SELECT value FROM settings WHERE name=?`, "component_registration:"+r.ComponentID).Scan(&raw)
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &existing); e != nil {
			return out, e
		}
		if existing.Installation != r.Installation || existing.Mode != r.Mode || existing.Connection != r.Connection || existing.ParentID != r.ParentID || existing.Runtime != r.Runtime {
			return out, errors.New("component installation, role, and connection mode are immutable")
		}
	} else if e != sql.ErrNoRows {
		return out, e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return out, e
	}
	_, e = tx.Exec(`INSERT INTO nodes(id,name,mode,hostname,tailnet_id,addresses,revoked,last_report) VALUES(?,?,?,?,?,?,0,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,hostname=excluded.hostname,tailnet_id=excluded.tailnet_id,addresses=excluded.addresses,revoked=0,last_report=excluded.last_report`, r.ComponentID, r.Name, r.Mode, r.Hostname, r.DeviceID, registrationJSON(r.Addresses), now.Unix())
	if e != nil {
		return out, e
	}
	_, e = tx.Exec(`INSERT INTO component_instances(component_id,parent_id,instance_id,device_id,generation,lease_until) VALUES(?,?,?,?,?,?) ON CONFLICT(component_id) DO UPDATE SET instance_id=excluded.instance_id,device_id=excluded.device_id,generation=excluded.generation,lease_until=excluded.lease_until`, out.ComponentID, r.ParentID, out.InstanceID, out.DeviceID, out.Generation, out.LeaseUntil)
	if e != nil {
		return out, e
	}
	_, e = tx.Exec(`INSERT INTO settings(name,value,secret) VALUES(?,?,0) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, "component_registration:"+r.ComponentID, string(b))
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) ComponentRegistration(id string) (Registration, error) {
	var r Registration
	e := s.GetJSON("component_registration:"+id, &r)
	return r, e
}
func (s *Store) CheckInstance(id, instance, device string, now time.Time) error {
	var n int
	e := s.db.QueryRow(`SELECT count(*) FROM component_instances WHERE component_id=? AND instance_id=? AND device_id=? AND lease_until>?`, id, instance, device, now.Unix()).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrInstanceConflict
	}
	return nil
}

// RenewInstance may reacquire an expired positive lease only for its unchanged
// verified holder. Replacement changes the instance/device pair; explicit release
// sets zero. Neither can be resurrected by this update.
func (s *Store) RenewInstance(id, instance, device string, now time.Time) error {
	// Authorize every call, but avoid a durable WAL flush for every config poll,
	// report and health request. Renew a one-minute lease at most every 15s.
	var until int64
	e := s.db.QueryRow(`SELECT lease_until FROM component_instances WHERE component_id=? AND instance_id=? AND device_id=? AND lease_until>0`, id, instance, device).Scan(&until)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrInstanceConflict
	}
	if e != nil {
		return e
	}
	if until > now.Add(45*time.Second).Unix() {
		return nil
	}
	r, e := s.db.Exec(`UPDATE component_instances SET lease_until=? WHERE component_id=? AND instance_id=? AND device_id=? AND lease_until>0 AND lease_until<=?`, now.Add(time.Minute).Unix(), id, instance, device, now.Add(45*time.Second).Unix())
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		// Another request may have renewed it. Recheck the verified holder;
		// replacement and explicit release must still fail closed.
		return s.CheckInstance(id, instance, device, now)
	}
	return e
}
func (s *Store) ReleaseInstance(id, instance, device string) error {
	_, e := s.db.Exec(`UPDATE component_instances SET lease_until=0 WHERE component_id=? AND instance_id=? AND device_id=?`, id, instance, device)
	return e
}
func (s *Store) BorrowedDevice(id string) (bool, error) {
	r, e := s.ComponentRegistration(id)
	if e != nil {
		return false, fmt.Errorf("component ownership unavailable: %w", e)
	}
	return r.Connection == "host", nil
}

func registrationJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func (s *Store) CurrentInstance(id string) (Instance, error) {
	var i Instance
	e := s.db.QueryRow(`SELECT component_id,instance_id,device_id,generation,lease_until FROM component_instances WHERE component_id=?`, id).Scan(&i.ComponentID, &i.InstanceID, &i.DeviceID, &i.Generation, &i.LeaseUntil)
	return i, e
}
