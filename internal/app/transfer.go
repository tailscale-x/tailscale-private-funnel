package app

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"tailscale.com/ipn"
)

// ControlTransfer contains complete stopped-control state. It must never be
// written to diagnostics, Kubernetes Events, command arguments or audit logs.
type ControlTransfer struct {
	Version  int                     `json:"version"`
	ID       string                  `json:"id"`
	Identity map[string][]byte       `json:"identity"`
	Database *store.TransferSnapshot `json:"database"`
}

func (b *ControlTransfer) Validate() error {
	if b == nil || b.Version != 1 || b.ID == "" || b.Database == nil {
		return errors.New("invalid control transfer")
	}
	if err := validateNodeBundle("control", core.Secret{Data: b.Identity}); err != nil {
		return err
	}
	var settings struct {
		Hostname string `json:"hostname"`
		Tailnet  string `json:"tailnet"`
	}
	if json.Unmarshal(b.Identity["control-settings.json"], &settings) != nil || settings.Hostname == "" || settings.Tailnet == "" {
		return errors.New("incomplete transferred control settings")
	}
	return nil
}

// ExportStoppedControl is deliberately offline: it never opens a tsnet.Server.
// Its caller must fence the source process before invoking it.
func ExportStoppedControl(ctx context.Context, o Options, identity map[string][]byte) (*ControlTransfer, error) {
	if identity != nil {
		var err error
		o, err = kubernetesNodeOptions("control", o)
		if err != nil {
			return nil, err
		}
		o, err = restoreNodeBundle("control", o, core.Secret{Data: identity})
		if err != nil {
			return nil, err
		}
	} else {
		identity = map[string][]byte{"mode": []byte("control"), "initialized": []byte("1")}
		for _, f := range nodeBundleFiles("control") {
			b, err := os.ReadFile(filepath.Join(o.DataDir, f))
			if err != nil {
				return nil, err
			}
			identity[f] = b
		}
		b, err := os.ReadFile(filepath.Join(o.StateDir, "tailscaled.state"))
		if err != nil {
			return nil, err
		}
		var state map[string][]byte
		if json.Unmarshal(b, &state) != nil {
			return nil, errors.New("invalid stopped Tailscale state")
		}
		for k, v := range state {
			identity[kubebroker.StateKey(ipn.StateKey(k))] = v
		}
		identity["control-settings.json"], _ = json.Marshal(map[string]any{"hostname": o.Hostname, "tailnet": o.Tailnet, "login_server": o.LoginServer, "accept_dns": o.AcceptDNS, "tags": o.Tags})
	}
	st, err := openRuntimeStore(o)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	if _, err = st.GetSecret("control_configuration_key"); err != nil {
		return nil, errors.New("source control signing identity missing")
	}
	_, key, err := st.ConfigurationKey()
	if err != nil {
		return nil, err
	}
	identity["control-signing-key"] = key
	id, err := store.NewResourceID()
	if err != nil {
		return nil, err
	}
	if len(identity["storage-marker"]) == 0 {
		identity["storage-marker"] = []byte(id)
	}
	snapshot, err := st.ExportTransfer(ctx)
	if err != nil {
		return nil, err
	}
	out := &ControlTransfer{Version: 1, ID: id, Identity: identity, Database: snapshot}
	return out, out.Validate()
}
func ExportKubernetesControl(ctx context.Context, o Options) (*ControlTransfer, error) {
	c, _, err := kubernetesClients(o)
	if err != nil {
		return nil, err
	}
	var s core.Secret
	if err = c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}, &s); err != nil {
		return nil, err
	}
	return ExportStoppedControl(ctx, o, s.Data)
}
func MarkControlTransferred(o Options, id string) error {
	st, err := openRuntimeStore(o)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.MarkTransferred(id)
}

// RestoreControl imports the logical dataset without contacting Tailscale. No
// startup or OAuth bootstrap is allowed until all restored state is validated.
func RestoreControl(ctx context.Context, o Options, b *ControlTransfer) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(o.StateDir, "joined")); !os.IsNotExist(err) {
		return errors.New("restore destination identity already exists")
	}
	if _, err := os.Stat(filepath.Join(o.DataDir, "master.key")); !os.IsNotExist(err) {
		return errors.New("restore destination encryption key already exists")
	}
	for _, p := range []string{o.DataDir, o.StateDir} {
		if err := os.MkdirAll(p, 0700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(o.DataDir, "master.key"), b.Identity["master.key"], 0600); err != nil {
		return err
	}
	// A failed restore leaves a durable fence, never a usable fresh installation.
	fence := filepath.Join(o.StateDir, "restore-incomplete")
	if err := os.WriteFile(fence, []byte(b.ID), 0600); err != nil {
		return err
	}
	o.RequireJoined = false
	o.Database.RequireExisting = false
	st, err := store.OpenConfigured(o.DataDir, o.DatabaseDir, o.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	if err = st.ImportTransfer(ctx, b.Database); err != nil {
		return err
	}
	_, key, err := st.ConfigurationKey()
	if err != nil {
		return err
	}
	if !bytes.Equal(key, b.Identity["control-signing-key"]) || len(key) != ed25519.PrivateKeySize {
		return errors.New("restored control signing identity mismatch")
	}
	if o.Database.Type == "postgres" {
		marker, e := st.StorageMarker(string(b.Identity["storage-marker"]))
		if e != nil {
			return e
		}
		if marker != string(b.Identity["storage-marker"]) {
			return errors.New("restored database binding mismatch")
		}
	} else {
		dir := o.DatabaseDir
		if dir == "" {
			dir = o.DataDir
		}
		if err = os.WriteFile(filepath.Join(dir, ".funnel-storage"), b.Identity["storage-marker"], 0600); err != nil {
			return err
		}
	}
	state := map[string][]byte{}
	for k, v := range b.Identity {
		if strings.HasPrefix(k, "tsnet.") {
			name, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(k, "tsnet."))
			if e != nil {
				return errors.New("invalid transferred Tailscale state key")
			}
			state[string(name)] = v
		}
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(o.StateDir, "tailscaled.state"), encoded, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(o.DataDir, "hostname"), b.Identity["hostname"], 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(o.StateDir, "joined"), []byte("persistent\n"), 0600); err != nil {
		return err
	}
	return os.Remove(fence)
}

func RestoreKubernetesControl(ctx context.Context, o Options, b *ControlTransfer) error {
	var err error
	o, err = kubernetesNodeOptions("control", o)
	if err != nil {
		return err
	}
	if err = prepareNodeStorage("control", o); err != nil {
		return err
	}
	c, _, err := kubernetesClients(o)
	if err != nil {
		return err
	}
	var secret core.Secret
	key := types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}
	if err = c.Get(ctx, key, &secret); err != nil {
		return err
	}
	if len(secret.Data) > 0 {
		return errors.New("destination identity Secret already contains state")
	}
	if err = RestoreControl(ctx, o, b); err != nil {
		return err
	}
	secret.Data = b.Identity
	// Ready URL and health are refreshed by the restored control, not asserted by
	// the installer from stale source observations.
	delete(secret.Data, "setup-url")
	if err = c.Update(ctx, &secret); err != nil {
		return errors.New("database restored but identity Secret save failed; destination remains suspended")
	}
	return nil
}
func MarkKubernetesControlTransferred(ctx context.Context, o Options, id string) error {
	c, _, err := kubernetesClients(o)
	if err != nil {
		return err
	}
	var secret core.Secret
	if err = c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}, &secret); err != nil {
		return err
	}
	o, err = kubernetesNodeOptions("control", o)
	if err != nil {
		return err
	}
	o, err = restoreNodeBundle("control", o, secret)
	if err != nil {
		return err
	}
	return MarkControlTransferred(o, id)
}
