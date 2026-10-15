package app

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kube"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"tailscale.com/ipn"
	"testing"
)

func TestKubernetesNodeStoppedImportAndPersistence(t *testing.T) {
	for _, mode := range []string{"control", "loadbalancer"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			srcData := filepath.Join(root, "source-data")
			srcState := filepath.Join(root, "source-state")
			os.MkdirAll(srcState, 0700)
			st, e := store.Open(srcData)
			if e != nil {
				t.Fatal(e)
			}
			_ = st.SetSecret("test-credential", "private-value")
			_ = st.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
			_, key, e := st.ConfigurationKey()
			if e != nil {
				t.Fatal(e)
			}
			st.Close()
			os.WriteFile(filepath.Join(srcData, "hostname"), []byte("original-node"), 0600)
			state, _ := json.Marshal(map[string][]byte{string(ipn.MachineKeyStateKey): []byte("test-machine")})
			os.WriteFile(filepath.Join(srcState, "tailscaled.state"), state, 0600)
			if mode == "loadbalancer" {
				b, _ := json.Marshal(savedConfig{Mode: mode, EnrollmentID: "node-1", DataDir: srcData, StateDir: srcState})
				os.WriteFile(filepath.Join(srcData, "config.json"), b, 0600)
				os.WriteFile(filepath.Join(srcData, "node-id"), []byte("node-1"), 0600)
				report.LoadOrCreate(filepath.Join(srcData, "report-identity.json"))
				os.MkdirAll(filepath.Join(srcData, "caddy"), 0700)
				os.WriteFile(filepath.Join(srcData, "caddy", "certificate.key"), []byte("certificate-material"), 0600)
			}
			o, e := kubernetesNodeOptions(mode, Options{IdentitySecret: "identity", KubeNamespace: "system", PersistentDir: filepath.Join(root, "persistent"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")})
			if e != nil {
				t.Fatal(e)
			}
			c := fake.NewClientBuilder().WithScheme(kube.Scheme()).WithObjects(&core.Secret{ObjectMeta: meta.ObjectMeta{Name: "identity", Namespace: "system"}}).Build()
			if e = importKubernetesNode(context.Background(), c, mode, o, srcState, srcData); e != nil {
				t.Fatal(e)
			}
			var secret core.Secret
			c.Get(context.Background(), types.NamespacedName{Namespace: "system", Name: "identity"}, &secret)
			if e = validateNodeBundle(mode, secret); e != nil {
				t.Fatal(e)
			}
			o.DataDir = filepath.Join(root, "replacement-data")
			o.StateDir = filepath.Join(root, "replacement-state")
			restored, e := restoreNodeBundle(mode, o, secret)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "control" {
				db, e := openRuntimeStore(restored)
				if e != nil {
					t.Fatal(e)
				}
				defer db.Close()
				value, e := db.GetSecret("test-credential")
				if e != nil || value != "private-value" {
					t.Fatal("lost encrypted credential", e)
				}
				_, after, e := db.ConfigurationKey()
				if e != nil || !bytes.Equal(after, key) {
					t.Fatal("changed signing key")
				}
				if _, e = os.Stat(filepath.Join(o.PersistentDir, "master.key")); !os.IsNotExist(e) {
					t.Fatal("encryption key on PVC")
				}
			} else {
				b, _ := os.ReadFile(filepath.Join(restored.DataDir, "caddy", "certificate.key"))
				if string(b) != "certificate-material" {
					t.Fatal("lost Caddy state")
				}
			}
			broken := secret.DeepCopy()
			broken.Data["master.key"] = []byte("invalid")
			if _, e = restoreNodeBundle(mode, o, *broken); e == nil {
				t.Fatal("accepted corrupt key")
			}
			os.Remove(nodeStorageMarker(mode, o))
			if _, e = restoreNodeBundle(mode, o, secret); e == nil {
				t.Fatal("accepted missing/mismatched PVC")
			}
		})
	}
}
