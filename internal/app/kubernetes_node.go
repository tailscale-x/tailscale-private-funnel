package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		return fmt.Errorf("existing storage without identity Secret: use stopped-state import")
	}
	o.RequireJoined = false
	o.BootstrapOnly = true
	o.NonInteractive = true
	o.InstallService = false
	if mode == "control" {
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
	return Serve(ctx, mode, o)
}

// ImportKubernetesNode requires a snapshot made with the source process stopped.
// It copies durable application storage, never enrolls or calls a DNS provider.
func ImportKubernetesNode(ctx context.Context, mode string, o Options, sourceState, sourceData string) error {
	o, e := kubernetesNodeOptions(mode, o)
	if e != nil {
		return e
	}
	c, _, e := kubernetesClients(o)
	if e != nil {
		return e
	}
	return importKubernetesNode(ctx, c, mode, o, sourceState, sourceData)
}
func importKubernetesNode(ctx context.Context, c client.Client, mode string, o Options, sourceState, sourceData string) error {
	var s core.Secret
	if e := c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}, &s); e != nil {
		return e
	}
	if len(s.Data) > 0 {
		return fmt.Errorf("identity Secret must be empty for explicit import")
	}
	var e error
	if e = prepareNodeStorage(mode, o); e != nil {
		return e
	}
	for _, f := range nodeBundleFiles(mode) {
		b, e := os.ReadFile(filepath.Join(sourceData, f))
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(o.DataDir, f), b, 0600); e != nil {
			return e
		}
	}
	b, e := os.ReadFile(filepath.Join(sourceState, "tailscaled.state"))
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(o.StateDir, "tailscaled.state"), b, 0600); e != nil {
		return e
	}
	if mode == "control" {
		if _, e = os.Stat(filepath.Join(o.DatabaseDir, "funnel.db")); e == nil {
			return fmt.Errorf("destination database already exists")
		}
		for _, f := range []string{"funnel.db", "funnel.db-wal", "funnel.db-shm"} {
			if e = copyNodeFile(filepath.Join(sourceData, f), filepath.Join(o.DatabaseDir, f)); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		o.Hostname = strings.TrimSpace(string(mustReadFile(filepath.Join(sourceData, "hostname"))))
		st, e := openRuntimeStore(o)
		if e != nil {
			return e
		}
		var settings map[string]any
		e = st.GetJSON("tailnet", &settings)
		st.Close()
		if e != nil {
			return e
		}
		o.Tailnet, _ = settings["name"].(string)
	} else {
		var cfg savedConfig
		if json.Unmarshal(mustReadFile(filepath.Join(o.DataDir, "config.json")), &cfg) != nil || cfg.Mode != mode {
			return fmt.Errorf("invalid source configuration")
		}
		cfg.DataDir, cfg.StateDir = o.DataDir, o.StateDir
		b, e = json.Marshal(cfg)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(o.DataDir, "config.json"), b, 0600); e != nil {
			return e
		}
		if entries, e := os.ReadDir(filepath.Join(o.PersistentDir, "caddy")); e != nil || len(entries) != 0 {
			return fmt.Errorf("destination Caddy storage must be empty")
		}
		e = filepath.WalkDir(filepath.Join(sourceData, "caddy"), func(path string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(filepath.Join(sourceData, "caddy"), path)
			if e != nil {
				return e
			}
			dst := filepath.Join(o.PersistentDir, "caddy", rel)
			if d.IsDir() {
				return os.MkdirAll(dst, 0700)
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlinks not allowed in Caddy snapshot")
			}
			return copyNodeFile(path, dst)
		})
		if e != nil {
			return e
		}
	}
	data, e := exportNodeBundle(mode, o)
	if e != nil {
		return e
	}
	s.Data = data
	return c.Update(ctx, &s)
}
func copyNodeFile(source, destination string) error {
	in, e := os.Open(source)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	closeErr := out.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func mustReadFile(path string) []byte { b, _ := os.ReadFile(path); return b }
