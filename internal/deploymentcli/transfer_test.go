package deploymentcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"io"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"os"
	"os/exec"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"tailscale.com/ipn"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
)

func TestTransferOutputSafetyAndAtomicity(t *testing.T) {
	root := t.TempDir()
	owned := filepath.Join(root, "state")
	os.Mkdir(owned, 0700)
	if _, err := validateTransferOutput(filepath.Join(owned, "restore.sh"), []string{owned}); err == nil {
		t.Fatal("source-contained output accepted")
	}
	link := filepath.Join(root, "linked-state")
	os.Symlink(owned, link)
	if _, err := validateTransferOutput(filepath.Join(link, "restore.sh"), []string{owned}); err == nil {
		t.Fatal("symlink containment bypass")
	}
	out := filepath.Join(root, "restore.sh")
	script := []byte("#!/bin/sh\nset -eu\n")
	if _, err := validateTransferOutput(out, []string{owned}); err != nil {
		t.Fatal(err)
	}
	if err := writeTransferInstaller(out, script); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(out)
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe output permissions")
	}
	if err := writeTransferInstaller(out, []byte("replacement")); err == nil {
		t.Fatal("existing file replaced")
	}
	b, _ := os.ReadFile(out)
	if !bytes.Equal(b, script) {
		t.Fatal("output corrupted")
	}
	symbolic := filepath.Join(root, "symbolic")
	os.Symlink(filepath.Join(root, "missing"), symbolic)
	if _, err := validateTransferOutput(symbolic, nil); err == nil {
		t.Fatal("dangling symlink accepted")
	}
}
func TestTransferCommandRequiresExplicitDestructiveInputs(t *testing.T) {
	for _, args := range [][]string{{"main"}, {"main", "cloud"}, {"main", "native", "--source", "native"}, {"main", "native", "--source", "native", "--out", "/tmp/not-written"}, {"main", "k8s", "--source", "native", "--out", "/tmp/not-written", "--yes"}} {
		c := TransferRemote("dev-test")
		c.SilenceErrors = true
		c.SilenceUsage = true
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatal("invalid input accepted", args)
		}
	}
}
func TestTransferInstallerUsesOnlyStdinAndSafeShell(t *testing.T) {
	payload := []byte(`{"secret":"' $(touch /tmp/funnel-transfer-must-not-exist) \\n FUNNEL_BOOTSTRAP"}`)
	for _, target := range []string{"native", "kubernetes"} {
		script := provision.TransferInstallerScript("v3.1.0", provision.ImageRepository+":v3.1.0", target, payload)
		if strings.Contains(script, "touch /tmp/") {
			t.Fatal("raw secret entered shell")
		}
		if !strings.Contains(script, base64.StdEncoding.EncodeToString(payload)) || !strings.Contains(script, "runtime restore") {
			t.Fatal("missing encoded stdin")
		}
		cmd := exec.Command("sh", "-n")
		cmd.Stdin = strings.NewReader(script)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(b))
		}
	}
}
func TestTransferPackageRejectsInvalidPayload(t *testing.T) {
	for _, raw := range []string{`{}`, `{"version":1,"extra":true}`, `{} {}`} {
		if _, err := readTransferPackage(strings.NewReader(raw), "native"); err == nil {
			t.Fatal("invalid package accepted")
		}
	}
	// Keep the wire encoding independent of SQL and Kubernetes YAML.
	data, _ := json.Marshal(transferPackage{Version: 1})
	if bytes.Contains(data, []byte("CREATE TABLE")) {
		t.Fatal("SQL embedded")
	}
}

type fakeTransferSource struct {
	bundle *app.ControlTransfer
	fail   string
	calls  []string
	out    string
}

func (s *fakeTransferSource) Settings() (string, string) { return "fixture", "example.ts.net" }
func (s *fakeTransferSource) Paths() []string            { return nil }
func (s *fakeTransferSource) Close()                     {}
func (s *fakeTransferSource) Fence(context.Context) error {
	s.calls = append(s.calls, "fence")
	if s.fail == "fence" {
		return errors.New("fixture failure")
	}
	return nil
}
func (s *fakeTransferSource) Export(context.Context) (*app.ControlTransfer, error) {
	s.calls = append(s.calls, "export")
	if s.fail == "export" {
		return nil, errors.New("fixture failure")
	}
	return s.bundle, nil
}
func (s *fakeTransferSource) Remove(_ context.Context, id, digest string) error {
	s.calls = append(s.calls, "remove")
	data, err := os.ReadFile(s.out)
	if err != nil {
		return errors.New("removal before durable output")
	}
	sum := sha256.Sum256(data)
	if digest != hex.EncodeToString(sum[:]) {
		return errors.New("incorrect receipt")
	}
	if s.fail == "remove" {
		return errors.New("fixture failure")
	}
	return nil
}
func transferFixture(t *testing.T) *transferPackage {
	t.Helper()
	p, err := provision.Resolve(&v1.DeploymentRequest{Mode: "control", Target: "native", ResourceName: "fixture", Hostname: "fixture", Tailnet: "example.ts.net"}, "", "dev-test")
	if err != nil {
		t.Fatal(err)
	}
	return &transferPackage{Version: 1, Deployment: p, Control: &app.ControlTransfer{Version: 1, ID: "transfer-fixture", Database: &store.TransferSnapshot{Version: 1}, Identity: map[string][]byte{"mode": []byte("control"), "initialized": []byte("1"), "master.key": make([]byte, 32), "hostname": []byte("fixture"), "storage-marker": []byte("binding"), "control-settings.json": []byte(`{"hostname":"fixture","tailnet":"example.ts.net"}`), "control-signing-key": make([]byte, 64), kubebroker.StateKey(ipn.MachineKeyStateKey): []byte("machine")}}}
}
func TestTransferFailureOrdering(t *testing.T) {
	for _, failure := range []string{"", "fence", "export", "write", "remove"} {
		t.Run(failure, func(t *testing.T) {
			p := transferFixture(t)
			out := filepath.Join(t.TempDir(), "restore.sh")
			s := &fakeTransferSource{bundle: p.Control, fail: failure, out: out}
			if failure == "write" {
				os.WriteFile(out, []byte("existing"), 0600)
			}
			_, err := executeTransfer(t.Context(), s, p.Deployment, out, io.Discard)
			if failure == "" && err != nil {
				t.Fatal(err)
			}
			if failure != "" && err == nil {
				t.Fatal("failure swallowed")
			}
			removed := strings.Contains(strings.Join(s.calls, ","), "remove")
			if (failure == "" || failure == "remove") != removed {
				t.Fatal("unsafe ordering", s.calls, err)
			}
			if failure == "remove" {
				if _, e := os.Stat(out); e != nil {
					t.Fatal("recovery installer removed")
				}
			}
		})
	}
}
func TestSavedTransferScriptExecutesThroughStdin(t *testing.T) {
	p := transferFixture(t)
	payload, _ := json.Marshal(p)
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	os.Mkdir(bin, 0700)
	// Stub only artifact download and the receiving executable. Execute the exact
	// generated POSIX payload pipeline without exposing secrets as argv.
	os.WriteFile(filepath.Join(bin, "python3"), []byte("#!/bin/sh\ncat >/dev/null\n"), 0700)
	receiver := `#!/bin/sh
printf '%s\n' "$*" > "$HOME/invocation"
cat > "$HOME/received.json"
`
	os.WriteFile(filepath.Join(bin, "tailscale-private-funnel"), []byte(receiver), 0700)
	script := provision.TransferInstallerScript(p.Deployment.Version, p.Deployment.Image, "native", payload)
	cmd := exec.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(), "HOME="+root, "XDG_BIN_HOME="+bin, "PATH="+bin+":"+os.Getenv("PATH"))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(b))
	}
	received, err := os.ReadFile(filepath.Join(root, "received.json"))
	if err != nil || !bytes.Equal(received, payload) {
		t.Fatal("stdin delivery changed", err)
	}
	call, _ := os.ReadFile(filepath.Join(root, "invocation"))
	if strings.TrimSpace(string(call)) != "native runtime restore" {
		t.Fatal("wrong receiver", string(call))
	}
	if _, err := readTransferPackage(bytes.NewReader(received), "native"); err != nil {
		t.Fatal(err)
	}
}

func TestTransferPreservesDependentControlBindings(t *testing.T) {
	scheme := runtime.NewScheme()
	funnel.AddToScheme(scheme)
	source := &funnel.FunnelControl{ObjectMeta: metav1.ObjectMeta{Name: "control", Namespace: "installation"}, Status: funnel.ComponentStatus{SetupURL: "http://100.64.0.1:8080"}}
	lb := &funnel.FunnelLoadBalancer{ObjectMeta: metav1.ObjectMeta{Name: "lb", Namespace: source.Namespace}, Spec: funnel.LoadBalancerSpec{ComponentSpec: funnel.ComponentSpec{ControlRef: &core.LocalObjectReference{Name: "control"}}}}
	other := &funnel.FunnelLoadBalancer{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: source.Namespace}, Spec: funnel.LoadBalancerSpec{ComponentSpec: funnel.ComponentSpec{ControlRef: &core.LocalObjectReference{Name: "another"}}}}
	k := fake.NewClientBuilder().WithScheme(scheme).WithObjects(source, lb, other).Build()
	if err := detachControlReferences(t.Context(), k, source); err != nil {
		t.Fatal(err)
	}
	if err := k.Get(t.Context(), client.ObjectKeyFromObject(lb), lb); err != nil {
		t.Fatal(err)
	}
	if lb.Spec.ControlRef != nil || lb.Spec.ManagementURL != source.Status.SetupURL {
		t.Fatal("control binding broken")
	}
	k.Get(t.Context(), client.ObjectKeyFromObject(other), other)
	if other.Spec.ControlRef == nil || other.Spec.ControlRef.Name != "another" {
		t.Fatal("unrelated control binding changed")
	}
}
