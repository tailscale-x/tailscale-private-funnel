package provision

import (
	"crypto/sha256"
	"fmt"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func releaseHTTP(bad bool) *http.Client {
	binary := []byte("fixture-binary")
	sum := fmt.Sprintf("%x", sha256.Sum256(binary))
	if bad {
		sum = strings.Repeat("0", 64)
	}
	return &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		body := string(binary)
		if strings.HasSuffix(r.URL.Path, "SHA256SUMS") {
			body = sum + "  tailscale-private-funnel-linux-amd64\n" + sum + "  tailscale-private-funnel-linux-arm64\n"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

func fixture(t *testing.T) *v1.DeploymentInstructions {
	t.Helper()
	p, e := Resolve(&v1.DeploymentRequest{Mode: "tailnet-gateway", Target: "native", ResourceName: "site", DisplayName: "Unicode 日本 ' label"}, "http://control:8080", "v2.0.0")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestReleaseBinaryChecksum(t *testing.T) {
	for _, bad := range []bool{false, true} {
		dir := t.TempDir()
		e := &Executor{HTTP: releaseHTTP(bad)}
		path, err := e.DownloadBinary(t.Context(), "v2.0.0", "", dir)
		if bad {
			if err == nil {
				t.Fatal("accepted invalid checksum")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil || string(b) != "fixture-binary" {
			t.Fatal("wrong artifact", err)
		}
	}
}
func TestRemoteScriptIsCompletePOSIXAndSecretsOnlyOnStdin(t *testing.T) {
	p := fixture(t)
	p.Credentials = &v1.BootstrapCredentials{AuthKey: "auth-secret", EnrollmentCode: "code-secret", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	s, e := RemoteOutput(p, "script", false)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "install.sh")
	os.WriteFile(path, []byte(s), 0600)
	if b, e := exec.Command("sh", "-n", path).CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	i := strings.Index(s, "<<'FUNNEL_BOOTSTRAP'")
	if i < 0 || strings.Contains(s[:i], "auth-secret") || !strings.Contains(s, "--bootstrap-stdin") {
		t.Fatal("credentials leaked outside protected bootstrap")
	}
	for _, old := range []string{"--print-command", " gateway join ", " provision ", "TS_AUTHKEY="} {
		if strings.Contains(s, old) {
			t.Fatal("obsolete command", old)
		}
	}
}
func TestDryRunScriptCannotExecute(t *testing.T) {
	s, e := RemoteOutput(fixture(t), "script", true)
	if e != nil {
		t.Fatal(e)
	}
	c := exec.Command("sh")
	c.Stdin = strings.NewReader(s)
	if e = c.Run(); e == nil {
		t.Fatal("dry run executable")
	}
}
func TestCommandsUseUnifiedSurface(t *testing.T) {
	p := fixture(t)
	for _, a := range []string{"create", "get", "update", "delete", "create-remote", "start", "stop", "restart", "logs"} {
		if !strings.Contains(p.Commands[a], "'native' 'gateways' '"+a+"' 'site'") {
			t.Fatal(a, p.Commands[a])
		}
	}
	if !strings.Contains(p.Commands["delete"], "'--yes'") {
		t.Fatal("delete lacks explicit approval")
	}
}
func TestStrictTargets(t *testing.T) {
	for _, r := range []*v1.DeploymentRequest{{Mode: "tailnet-gateway", Target: "local"}, {Mode: "operator", Target: "native"}, {Mode: "k8s-gateway", Target: "native"}, {Mode: "tailnet-gateway", Target: "kubernetes"}, {Mode: "docker-gateway", Target: "native", Runtime: "docker"}, {Mode: "tailnet-gateway", Target: "native", StateDir: "/"}, {Mode: "tailnet-gateway", Target: "native", StateDir: "/a", DataDir: "/a/b"}} {
		if _, e := Resolve(r, "http://control:8080", "v2.0.0"); e == nil {
			t.Fatalf("accepted %v", r)
		}
	}
}

func TestKubernetesStorageInstructions(t *testing.T) {
	p, e := Resolve(&v1.DeploymentRequest{Mode: "control", Target: "kubernetes", ResourceName: "main", Context: "cluster", Tailnet: "example.ts.net", StorageClass: "openebs-lvm", StorageSize: "4Gi"}, "", "dev-test")
	if e != nil {
		t.Fatal(e)
	}
	command := p.Commands["create"]
	if !strings.Contains(command, "--storage-class") || !strings.Contains(command, "openebs-lvm") || !strings.Contains(command, "4Gi") {
		t.Fatal(command)
	}
	_, e = Resolve(&v1.DeploymentRequest{Mode: "control", Target: "kubernetes", ResourceName: "main", Context: "cluster", Tailnet: "example.ts.net", StorageSize: "-1Gi"}, "", "dev-test")
	if e == nil {
		t.Fatal("negative volume accepted")
	}
}
