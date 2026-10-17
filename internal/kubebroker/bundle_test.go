package kubebroker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/kube"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"tailscale.com/ipn"
)

func TestControllerRestoreRequiresExistingMachineIdentity(t *testing.T) {
	ctx := context.Background()
	s := &core.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "system", Name: "controller"}, Data: map[string][]byte{}}
	for _, name := range BundleFiles {
		s.Data[name] = []byte("stored-" + name)
	}
	c := fake.NewClientBuilder().WithScheme(kube.Scheme()).WithObjects(s).Build()
	data, state := t.TempDir(), t.TempDir()
	if err := RestoreAgentBundle(ctx, c, "system", "controller", data, state); err == nil {
		t.Fatal("corrupt identity accepted")
	}
	if _, err := os.Stat(filepath.Join(data, "config.json")); !os.IsNotExist(err) {
		t.Fatal("wrote partial bundle before validating identity")
	}
	_ = c.Get(ctx, types.NamespacedName{Namespace: "system", Name: "controller"}, s)
	s.Data[stateKey(ipn.MachineKeyStateKey)] = []byte("existing-machine-key")
	if err := c.Update(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := RestoreAgentBundle(ctx, c, "system", "controller", data, state); err != nil {
		t.Fatal(err)
	}
	for _, name := range BundleFiles {
		b, err := os.ReadFile(filepath.Join(data, name))
		if err != nil || string(b) != string(s.Data[name]) {
			t.Fatal(name, err)
		}
	}
}
