package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"tailscale.com/ipn"
)

func openRuntimeStore(o Options) (*store.Store, error) {
	if o.DatabaseDir != "" {
		return store.OpenDatabase(o.DataDir, o.DatabaseDir)
	}
	return store.Open(o.DataDir)
}

func kubernetesNodeOptions(mode string, o Options) (Options, error) {
	if mode != "control" && mode != "loadbalancer" {
		return o, fmt.Errorf("Kubernetes node storage requires control or loadbalancer")
	}
	if o.IdentitySecret == "" || o.KubeNamespace == "" || o.PersistentDir == "" {
		return o, fmt.Errorf("namespace, identity Secret and persistent directory required")
	}
	if o.StateDir == "" {
		o.StateDir = "/var/lib/tailscale-private-funnel/state"
	}
	if o.DataDir == "" {
		o.DataDir = "/var/lib/tailscale-private-funnel/data"
	}
	if mode == "control" {
		o.DatabaseDir = filepath.Join(o.PersistentDir, "sqlite")
	}
	return o, nil
}

func prepareNodeStorage(mode string, o Options) error {
	for _, dir := range []string{o.StateDir, o.DataDir, o.PersistentDir} {
		if e := os.MkdirAll(dir, 0700); e != nil {
			return e
		}
	}
	if mode == "control" {
		return os.MkdirAll(o.DatabaseDir, 0700)
	}
	dir := filepath.Join(o.PersistentDir, "caddy")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	path := filepath.Join(o.DataDir, "caddy")
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return os.Symlink(dir, path)
	} else if e != nil {
		return e
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != dir {
		return fmt.Errorf("Caddy storage must point to its dedicated PVC")
	}
	return nil
}

func nodeBundleFiles(mode string) []string {
	files := []string{"master.key", "hostname"}
	if mode == "loadbalancer" {
		files = append(files, "config.json", "node-id", "report-identity.json")
	}
	return files
}

func validateNodeBundle(mode string, s core.Secret) error {
	if string(s.Data["mode"]) != mode || string(s.Data["initialized"]) != "1" {
		return fmt.Errorf("identity Secret is not initialized for %s", mode)
	}
	for _, f := range nodeBundleFiles(mode) {
		if len(s.Data[f]) == 0 {
			return fmt.Errorf("identity Secret missing %s", f)
		}
	}
	if len(s.Data["master.key"]) != 32 {
		return fmt.Errorf("invalid database encryption key")
	}
	if len(s.Data[kubebroker.StateKey(ipn.MachineKeyStateKey)]) == 0 {
		return fmt.Errorf("identity Secret missing Tailscale machine key")
	}
	if len(s.Data["storage-marker"]) == 0 {
		return fmt.Errorf("identity Secret missing storage binding")
	}
	if mode == "control" && (len(s.Data["control-settings.json"]) == 0 || len(s.Data["control-signing-key"]) != ed25519.PrivateKeySize) {
		return fmt.Errorf("invalid control settings or signing key")
	}
	if mode == "loadbalancer" {
		var cfg savedConfig
		if json.Unmarshal(s.Data["config.json"], &cfg) != nil || cfg.Mode != mode || cfg.EnrollmentID != strings.TrimSpace(string(s.Data["node-id"])) {
			return fmt.Errorf("invalid enrolled load-balancer configuration")
		}
	}
	return nil
}

func exportNodeBundle(mode string, o Options) (map[string][]byte, error) {
	data := map[string][]byte{"mode": []byte(mode), "initialized": []byte("1")}
	for _, f := range nodeBundleFiles(mode) {
		b, e := os.ReadFile(filepath.Join(o.DataDir, f))
		if e != nil {
			return nil, e
		}
		data[f] = b
	}
	var state map[string][]byte
	b, e := os.ReadFile(filepath.Join(o.StateDir, "tailscaled.state"))
	if e != nil {
		return nil, e
	}
	if json.Unmarshal(b, &state) != nil || len(state) == 0 {
		return nil, fmt.Errorf("invalid stopped Tailscale state")
	}
	for k, v := range state {
		data[kubebroker.StateKey(ipn.StateKey(k))] = v
	}
	if mode == "control" {
		st, e := openRuntimeStore(o)
		if e != nil {
			return nil, e
		}
		defer st.Close()
		_, key, e := st.ConfigurationKey()
		if e != nil {
			return nil, e
		}
		data["control-signing-key"] = key
		data["control-settings.json"], e = json.Marshal(map[string]any{"hostname": o.Hostname, "tailnet": o.Tailnet, "login_server": o.LoginServer, "accept_dns": o.AcceptDNS, "tags": o.Tags})
		if e != nil {
			return nil, e
		}
	}
	marker, e := store.NewResourceID()
	if e != nil {
		return nil, e
	}
	data["storage-marker"] = []byte(marker)
	if e = os.WriteFile(nodeStorageMarker(mode, o), []byte(marker), 0600); e != nil {
		return nil, e
	}
	return data, nil
}
func nodeStorageMarker(mode string, o Options) string {
	if mode == "control" {
		return filepath.Join(o.DatabaseDir, ".funnel-storage")
	}
	return filepath.Join(o.PersistentDir, "caddy", ".funnel-storage")
}

func restoreNodeBundle(mode string, o Options, s core.Secret) (Options, error) {
	if e := validateNodeBundle(mode, s); e != nil {
		return o, e
	}
	marker, e := os.ReadFile(nodeStorageMarker(mode, o))
	if e != nil || !bytes.Equal(marker, s.Data["storage-marker"]) {
		return o, fmt.Errorf("persistent storage missing or does not match identity Secret; restore matching backup")
	}
	if mode == "control" {
		if info, e := os.Stat(filepath.Join(o.DatabaseDir, "funnel.db")); e != nil || info.Size() == 0 {
			return o, fmt.Errorf("persistent SQLite database missing")
		}
	}
	if e := prepareNodeStorage(mode, o); e != nil {
		return o, e
	}
	for _, f := range nodeBundleFiles(mode) {
		if e := os.WriteFile(filepath.Join(o.DataDir, f), s.Data[f], 0600); e != nil {
			return o, e
		}
	}
	if mode == "control" {
		var settings struct {
			Hostname    string   `json:"hostname"`
			Tailnet     string   `json:"tailnet"`
			LoginServer string   `json:"login_server"`
			AcceptDNS   bool     `json:"accept_dns"`
			Tags        []string `json:"tags"`
		}
		if e := json.Unmarshal(s.Data["control-settings.json"], &settings); e != nil {
			return o, e
		}
		o.Hostname, o.Tailnet, o.LoginServer, o.AcceptDNS, o.Tags = settings.Hostname, settings.Tailnet, settings.LoginServer, settings.AcceptDNS, settings.Tags
		st, e := openRuntimeStore(o)
		if e != nil {
			return o, e
		}
		defer st.Close()
		_, key, e := st.ConfigurationKey()
		if e != nil {
			return o, e
		}
		if !bytes.Equal(key, s.Data["control-signing-key"]) {
			return o, fmt.Errorf("database signing key does not match identity Secret")
		}
	} else {
		o.ConfigFile = filepath.Join(o.DataDir, "config.json")
		if _, _, e := report.LoadOrCreate(filepath.Join(o.DataDir, "report-identity.json")); e != nil {
			return o, e
		}
	}
	if e := os.WriteFile(filepath.Join(o.StateDir, "joined"), []byte("persistent\n"), 0600); e != nil {
		return o, e
	}
	o.RequireJoined = false
	return o, nil
}

func BootstrapKubernetesNode(ctx context.Context, mode string, o Options) error {
	o, e := kubernetesNodeOptions(mode, o)
	if e != nil {
		return e
	}
	c, _, e := kubernetesClients(o)
	if e != nil {
		return e
	}
	var s core.Secret
	if e = c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}, &s); e != nil {
		return fmt.Errorf("precreated identity Secret required: %w", e)
	}
	if len(s.Data) > 0 {
		_, e = restoreNodeBundle(mode, o, s)
		return e
	}
	if e = prepareNodeStorage(mode, o); e != nil {
		return e
	}
	existing := filepath.Join(o.PersistentDir, "caddy", ".funnel-storage")
	if mode == "control" {
		existing = filepath.Join(o.DatabaseDir, "funnel.db")
	}
	if _, e = os.Stat(existing); e == nil {
		return fmt.Errorf("existing storage has no matching identity Secret; restore matching state or explicitly delete and recreate")
	}
	o.RequireJoined = false
	o.BootstrapOnly = true
	o.NonInteractive = true
	if mode == "control" {
		if b, err := os.ReadFile(filepath.Join(o.BootstrapSecretDir, "administrators")); err == nil {
			if err = json.Unmarshal(b, &o.Administrators); err != nil {
				return fmt.Errorf("invalid bootstrap administrators")
			}
		}
		o.OAuthClientIDFile = filepath.Join(o.BootstrapSecretDir, "client-id")
		o.OAuthClientSecretFile = filepath.Join(o.BootstrapSecretDir, "client-secret")
		e = Setup(ctx, mode, o)
	} else {
		e = Deploy(ctx, mode, o)
	}
	if e != nil {
		return e
	}
	data, e := exportNodeBundle(mode, o)
	if e != nil {
		return e
	}
	s.Data = data
	if e = c.Update(ctx, &s); e != nil {
		return fmt.Errorf("persist identity Secret: %w", e)
	}
	return os.Remove(filepath.Join(o.StateDir, "tailscaled.state"))
}

func ServeKubernetesNode(ctx context.Context, mode string, o Options) error {
	o, e := kubernetesNodeOptions(mode, o)
	if e != nil {
		return e
	}
	c, _, e := kubernetesClients(o)
	if e != nil {
		return e
	}
	var s core.Secret
	if e = c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}, &s); e != nil {
		return e
	}
	o, e = restoreNodeBundle(mode, o, s)
	if e != nil {
		return e
	}
	o.TSStore = kubebroker.SecretStore{Client: c, Namespace: o.KubeNamespace, Name: o.IdentitySecret}
	if mode == "control" {
		go publishKubernetesControlState(ctx, o)
	}
	return Serve(ctx, mode, o)
}
