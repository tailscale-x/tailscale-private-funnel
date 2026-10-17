package kubebroker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"tailscale.com/ipn"
)

var BundleFiles = []string{"config.json", "node-id", "report-identity.json", "cluster-id"}

func StoreAgentBundle(ctx context.Context, c client.Client, ns, name, dataDir, stateDir string) error {
	s := core.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Data: map[string][]byte{}}
	for _, f := range BundleFiles {
		b, e := os.ReadFile(filepath.Join(dataDir, f))
		if e != nil {
			return e
		}
		s.Data[f] = b
	}
	b, e := os.ReadFile(filepath.Join(stateDir, "tailscaled.state"))
	if e != nil {
		return e
	}
	var state map[string][]byte
	if json.Unmarshal(b, &state) != nil || len(state) == 0 {
		return fmt.Errorf("invalid agent identity")
	}
	for k, v := range state {
		s.Data[stateKey(ipn.StateKey(k))] = v
	}
	if uid := os.Getenv("FUNNEL_OWNER_UID"); uid != "" {
		s.Labels = map[string]string{"tailscale-private-funnel.io/owner-uid": uid}
	}
	var existing core.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &existing); err == nil {
		if len(existing.Data) > 0 {
			return fmt.Errorf("agent identity already initialized")
		}
		existing.Data = s.Data
		return c.Update(ctx, &existing)
	}
	return c.Create(ctx, &s)
}
func RestoreAgentBundle(ctx context.Context, c client.Client, ns, name, dataDir, stateDir string) error {
	var s core.Secret
	if e := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &s); e != nil {
		return e
	}
	if len(s.Data[stateKey(ipn.MachineKeyStateKey)]) == 0 {
		return fmt.Errorf("agent identity missing Tailscale machine key; restore its Secret")
	}
	if e := os.MkdirAll(dataDir, 0700); e != nil {
		return e
	}
	if e := os.MkdirAll(stateDir, 0700); e != nil {
		return e
	}
	for _, f := range BundleFiles {
		if len(s.Data[f]) == 0 {
			return fmt.Errorf("agent bundle missing %s", f)
		}
		if e := os.WriteFile(filepath.Join(dataDir, f), s.Data[f], 0600); e != nil {
			return e
		}
	}
	return os.WriteFile(filepath.Join(stateDir, "joined"), []byte("persistent\n"), 0600)
}
