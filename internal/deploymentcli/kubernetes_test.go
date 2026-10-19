package deploymentcli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
)

func TestNewCRReferencesBootstrapWithoutCredentials(t *testing.T) {
	for _, kind := range []string{"controls", "gateways", "loadbalancers"} {
		o := newCR(kind, "site", Flags{Version: "dev-test", Namespace: "funnel-system", Tailnet: "example.ts.net"})
		if crSpec(o).KeyIssuerRef.Name != "" || crSpec(o).Image != "ghcr.io/tailscale-x/tailscale-private-funnel:dev-test" {
			t.Fatal(o)
		}
		if g, ok := o.(*funnel.FunnelGateway); ok && g.Spec.GatewayClass != "tailscale-private-funnel-site" {
			t.Fatal("renderer/CLI class mismatch")
		}
	}
}
func TestPrivateCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	os.WriteFile(path, []byte("private"), 0644)
	os.Chmod(path, 0644) // Explicitly public even when the test runner uses umask 0077.
	if _, e := privateFile(path); e == nil {
		t.Fatal("public credentials accepted")
	}
	os.Chmod(path, 0600)
	if _, e := privateFile(path); e != nil {
		t.Fatal(e)
	}
}

func TestManifestCommandSeparatesRegistryDiagnostics(t *testing.T) {
	b, e := commandOutput(t.Context(), os.Args[0], []string{"-test.run=TestManifestHelperProcess"}, append(os.Environ(), "FUNNEL_CMD_HELPER=1"))
	if e != nil {
		t.Fatal(e)
	}
	if string(b) != "apiVersion: v1\nkind: List\nitems: []\n" {
		t.Fatalf("manifest polluted by diagnostics: %q", b)
	}
}
func TestManifestHelperProcess(t *testing.T) {
	if os.Getenv("FUNNEL_CMD_HELPER") != "1" {
		return
	}
	fmt.Fprintln(os.Stderr, "Pulled: ghcr.io/example/chart")
	fmt.Print("apiVersion: v1\nkind: List\nitems: []\n")
	os.Exit(0)
}

func TestPlacementFilesRejectUnknownOrExtraValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "placement.json")
	for _, value := range []string{`{"credentials":"secret"}`, `{"tolerations":[]} {}`} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readPlacement(path); err == nil {
			t.Fatal("accepted invalid placement", value)
		}
	}
	os.WriteFile(path, []byte(`{"tolerations":[{"operator":"Exists","effect":"NoSchedule"}]}`), 0600)
	p, err := readPlacement(path)
	if err != nil || len(p.Tolerations) != 1 {
		t.Fatal(p, err)
	}
}

func TestControlStorageIsExplicitInCR(t *testing.T) {
	o := newCR("controls", "main", Flags{Namespace: "funnel-system", Version: "dev-test", StorageClass: "openebs-lvm", StorageSize: "4Gi", Tailnet: "example.ts.net"})
	s := crSpec(o).Storage
	if s.StorageClassName == nil || *s.StorageClassName != "openebs-lvm" || s.Size != "4Gi" {
		t.Fatal(s)
	}
}
