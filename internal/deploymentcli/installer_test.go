package deploymentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type issuerFixture struct {
	funnelv1connect.UnimplementedDeploymentServiceHandler
	calls int
}

func (s *issuerFixture) Authorize(_ context.Context, r *connect.Request[v1.DeploymentRequest]) (*connect.Response[v1.DeploymentInstructions], error) {
	s.calls++
	p, err := provision.Resolve(r.Msg, "http://private-control:8080", "dev-test")
	if err != nil {
		return nil, err
	}
	p.Credentials = &v1.BootstrapCredentials{AuthKey: "tskey-fixture", ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	return connect.NewResponse(p), nil
}
func TestCreationOnlyGeneratesWithoutTargetAccess(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	t.Setenv("PATH", "/no-target-tools")
	for _, target := range []string{"native", "kubernetes"} {
		t.Run(target, func(t *testing.T) {
			issuer := new(issuerFixture)
			_, h := funnelv1connect.NewDeploymentServiceHandler(issuer)
			server := httptest.NewServer(h)
			defer server.Close()
			root := Native("dev-test")
			args := []string{"gateways", "create", "site", "--type", "tailnet", "--control-url", server.URL, "--output", "json", "--wait", "--timeout", "20m"}
			if target == "kubernetes" {
				root = Kubernetes("dev-test")
				args = []string{"gateways", "create", "site", "--context", "not-on-this-machine", "--namespace", "funnel-test", "--control-url", server.URL, "--output", "json", "--wait", "--timeout", "20m"}
			}
			root.SetArgs(args)
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if issuer.calls != map[string]int{"native": 1, "kubernetes": 0}[target] {
				t.Fatalf("authorization calls: %d", issuer.calls)
			}
			p, err := readInstallerBundle(bytes.NewReader(out.Bytes()), target)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Deployment.Wait || p.Deployment.TimeoutSeconds != 1200 {
				t.Fatal("execution wait options lost")
			}
			if !strings.Contains(p.Script, "runtime install") || strings.Contains(p.Script, "create-remote") {
				t.Fatal("wrong installer")
			}
			if p.ManagementUrl != map[string]string{"native": "http://private-control:8080", "kubernetes": server.URL}[target] {
				t.Fatal("lost private control address")
			}
		})
	}
}
func TestControlAndOperatorGenerationReadsNoTargetFiles(t *testing.T) {
	for _, tc := range []struct {
		target string
		args   []string
	}{
		{"native", []string{"controls", "create", "main", "--tailnet", "example.ts.net", "--oauth-client-id-file", "/target/only/id", "--oauth-client-secret-file", "/target/only/secret", "--output", "json"}},
		{"kubernetes", []string{"operator", "create", "--context", "remote-only", "--managed-namespace", "funnel-test", "--output", "json"}},
		{"kubernetes", []string{"controls", "create", "main", "--context", "remote-only", "--tailnet", "example.ts.net", "--key-issuer-secret", "control-issuer", "--services-secret", "services", "--users-secret", "users", "--output", "json"}},
	} {
		t.Run(tc.target+tc.args[0], func(t *testing.T) {
			t.Setenv("KUBECONFIG", "/does-not-exist")
			root := Native("dev-test")
			if tc.target == "kubernetes" {
				root = Kubernetes("dev-test")
			}
			root.SetArgs(tc.args)
			var out bytes.Buffer
			root.SetOut(&out)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if _, err := readInstallerBundle(bytes.NewReader(out.Bytes()), tc.target); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestInstallerRejectsInvalidBundlesBeforeExecution(t *testing.T) {
	p, err := provision.Resolve(&v1.DeploymentRequest{Mode: "tailnet-gateway", Target: "native", ResourceName: "site"}, "http://control:8080", "dev-test")
	if err != nil {
		t.Fatal(err)
	}
	p.Credentials = &v1.BootstrapCredentials{AuthKey: "fixture", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	for _, tc := range []struct {
		name   string
		mutate func(*v1.DeploymentInstructions)
	}{
		{"expired", func(p *v1.DeploymentInstructions) { p.Credentials.ExpiresAt = time.Now().Unix() - 1 }},
		{"missing-key", func(p *v1.DeploymentInstructions) { p.Credentials.AuthKey = "" }},
		{"target", func(p *v1.DeploymentInstructions) { p.Deployment.Target = "kubernetes" }},
		{"image", func(p *v1.DeploymentInstructions) { p.Image = "untrusted:image" }},
		{"namespace", func(p *v1.DeploymentInstructions) { p.Namespace = "other" }},
		{"version", func(p *v1.DeploymentInstructions) { p.Deployment.Version = "v1.0.0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := proto.Clone(p).(*v1.DeploymentInstructions)
			tc.mutate(q)
			b, _ := protojson.Marshal(q)
			if _, err := readInstallerBundle(bytes.NewReader(b), "native"); err == nil {
				t.Fatal("accepted invalid bundle")
			}
		})
	}
	b, _ := json.Marshal(p)
	if _, err := readInstallerBundle(bytes.NewReader(b), "native"); err != nil {
		t.Fatal(err)
	}
}
func TestKubernetesDryRunsNeverLoadKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "/missing/config")
	for _, kind := range []string{"gateways", "loadbalancers", "controls", "operator"} {
		args := []string{kind, "create"}
		if kind != "operator" {
			args = append(args, "site")
		}
		args = append(args, "--context", "remote", "--dry-run", "--output", "script")
		switch kind {
		case "operator":
			args = append(args, "--managed-namespace", "funnel-test")
		case "loadbalancers":
			args = append(args, "--public-hostname", "edge.example.com", "--certificate-email", "test@example.com")
		}
		if kind == "gateways" || kind == "loadbalancers" {
			args = append(args, "--control-url", "http://unreachable.invalid:8080")
		}
		c := Kubernetes("dev-test")
		c.SetArgs(args)
		var out bytes.Buffer
		c.SetOut(&out)
		if err := c.Execute(); err != nil {
			t.Fatal(kind, err)
		}
		if !strings.Contains(out.String(), "exit 1") {
			t.Fatal("executable dry run")
		}
	}
}
func TestPlacementTravelsInBundle(t *testing.T) {
	path := t.TempDir() + "/placement.json"
	if err := os.WriteFile(path, []byte(`{"nodeSelector":{"pool":"edge"},"tolerations":[{"operator":"Exists"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	in := new(v1.DeploymentRequest)
	if err := bundlePlacement(in, Flags{PlacementFile: path}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	object := newCR("gateways", "site", Flags{})
	if err := applyBundlePlacement(object, in); err != nil {
		t.Fatal(err)
	}
	if crSpec(object).Placement.NodeSelector["pool"] != "edge" {
		t.Fatal("placement lost")
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("broken pipe") }

func TestControlDryRunJSONCannotBeInstalled(t *testing.T) {
	c := Native("dev-test")
	c.SetArgs([]string{"controls", "create", "main", "--dry-run", "--output", "json"})
	var out bytes.Buffer
	c.SetOut(&out)
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := readInstallerBundle(bytes.NewReader(out.Bytes()), "native"); err == nil || !strings.Contains(err.Error(), "dry-run") {
		t.Fatal("dry-run installer accepted", err)
	}
}

func TestKubernetesInstallerPermissionFailureIsActionable(t *testing.T) {
	mutations := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"fixture denied","reason":"Forbidden","code":403}`)
	}))
	defer api.Close()
	path := t.TempDir() + "/config"
	config := "apiVersion: v1\nkind: Config\nclusters:\n- name: fixture\n  cluster:\n    server: " + api.URL + "\ncontexts:\n- name: denied\n  context:\n    cluster: fixture\n    user: fixture\nusers:\n- name: fixture\n  user: {}\n"
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", path)
	p, err := provision.Resolve(&v1.DeploymentRequest{Mode: "k8s-gateway", Target: "kubernetes", ResourceName: "site", Context: "denied"}, "http://unreachable.private.invalid:8080", "dev-test")
	if err != nil {
		t.Fatal(err)
	}
	p.Credentials = nil
	b, _ := json.Marshal(p)
	cmd := Installer("kubernetes")
	cmd.SetIn(bytes.NewReader(b))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err = cmd.ExecuteContext(t.Context())
	if err == nil || !strings.Contains(err.Error(), "context and permissions") {
		t.Fatal("wrong permission diagnostic", err)
	}
	if mutations != 0 {
		t.Fatal("permission failure mutated cluster")
	}
}
