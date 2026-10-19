package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
)

func openRuntimeStore(o Options) (*store.Store, error) {
	return store.OpenConfigured(o.DataDir, o.DatabaseDir, o.Database)
}
func kubernetesNodeOptions(mode string, o Options) (Options, error) {
	if mode != "control" && mode != "loadbalancer" {
		return o, errors.New("control or loadbalancer required")
	}
	if o.KubeNamespace == "" {
		return o, errors.New("Kubernetes namespace required")
	}
	if o.StateDir == "" {
		o.StateDir = "/var/lib/tailscale-private-funnel/state"
	}
	if o.DataDir == "" {
		o.DataDir = "/var/lib/tailscale-private-funnel/data"
	}
	o.Ephemeral = true
	o.TailscaleConnection = "embedded"
	if mode == "control" && o.Database.Type == "postgres" {
		o.PersistentDir = ""
	}
	if mode == "control" && o.Database.Type != "postgres" {
		if o.PersistentDir == "" {
			return o, errors.New("SQLite PVC path required")
		}
		o.DatabaseDir = filepath.Join(o.PersistentDir, "sqlite")
	}
	if mode == "loadbalancer" && o.PersistentDir == "" {
		return o, errors.New("Caddy certificate PVC path required")
	}
	return o, nil
}
func prepareNodeStorage(mode string, o Options) error {
	for _, d := range []string{o.StateDir, o.DataDir, o.PersistentDir, o.DatabaseDir} {
		if d != "" {
			if e := os.MkdirAll(d, 0700); e != nil {
				return e
			}
		}
	}
	if mode == "loadbalancer" {
		dir := filepath.Join(o.PersistentDir, "caddy")
		if e := os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		link := filepath.Join(o.DataDir, "caddy")
		if _, e := os.Lstat(link); os.IsNotExist(e) {
			return os.Symlink(dir, link)
		}
		p, e := filepath.EvalSymlinks(link)
		if e != nil || p != dir {
			return errors.New("Caddy storage must use its dedicated PVC")
		}
	}
	return nil
}

// Control material contains application encryption/binding only, never a tsnet identity.
func validateControlMaterial(mode string, s core.Secret) error {
	if mode != "control" || len(s.Data["master.key"]) != 32 || len(s.Data["storage-marker"]) == 0 {
		return errors.New("matching control encryption and storage material required")
	}
	return nil
}
func nodeStorageMarker(_ string, o Options) string {
	return filepath.Join(o.DatabaseDir, ".funnel-storage")
}
func bindControlMaterial(mode string, o Options, s core.Secret) (Options, error) {
	if e := validateControlMaterial(mode, s); e != nil {
		return o, e
	}
	if e := prepareNodeStorage(mode, o); e != nil {
		return o, e
	}
	if e := os.WriteFile(filepath.Join(o.DataDir, "master.key"), s.Data["master.key"], 0600); e != nil {
		return o, e
	}
	var marker []byte
	if o.Database.Type == "postgres" {
		st, e := openRuntimeStore(o)
		if e != nil {
			return o, e
		}
		v, e := st.StorageMarker(string(s.Data["storage-marker"]))
		st.Close()
		if e != nil {
			return o, e
		}
		marker = []byte(v)
	} else {
		p := nodeStorageMarker(mode, o)
		b, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			if info, stat := os.Stat(filepath.Join(o.DatabaseDir, "funnel.db")); stat == nil && info.Size() > 0 {
				return o, errors.New("existing SQLite database lacks matching storage binding")
			}
			e = os.WriteFile(p, s.Data["storage-marker"], 0600)
			b = s.Data["storage-marker"]
		}
		if e != nil {
			return o, e
		}
		marker = b
	}
	if !bytes.Equal(marker, s.Data["storage-marker"]) {
		return o, errors.New("control database and encryption material mismatch")
	}

	return o, nil
}
func ServeKubernetesNode(ctx context.Context, mode string, o Options) error {
	o, e := kubernetesNodeOptions(mode, o)
	if e != nil {
		return e
	}
	if e = prepareNodeStorage(mode, o); e != nil {
		return e
	}
	if mode == "control" {
		if e = loadControlMaterial(&o); e != nil {
			return e
		}
	}
	return Serve(ctx, mode, o)
}
func loadControlMaterial(o *Options) error {
	dir := o.MaterialDir
	if dir == "" {
		dir = "/etc/funnel-material"
	}
	data := map[string][]byte{}
	for _, name := range []string{"master.key", "storage-marker"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			return fmt.Errorf("control application material unavailable: %w", e)
		}
		data[name] = b
	}
	resolved, e := bindControlMaterial("control", *o, core.Secret{Data: data})
	if e == nil {
		*o = resolved
	}
	return e
}
