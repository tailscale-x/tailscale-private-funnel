package chart_test

import (
	"bytes"
	"os/exec"
	"testing"
)

func render(t *testing.T, args ...string) []byte {
	t.Helper()
	if _, e := exec.LookPath("helm"); e != nil {
		t.Skip("Helm required")
	}
	all := append([]string{"template", "fixture", ".", "--set", "managedNamespaces[0]=installation-a", "--set", "managedNamespaces[1]=installation-b"}, args...)
	b, e := exec.Command("helm", all...).CombinedOutput()
	if e != nil {
		t.Fatal(e, string(b))
	}
	return b
}
func TestOperatorChartScope(t *testing.T) {
	b := render(t)
	for _, s := range []string{"replicas: 1", "type: Recreate", "k8s", "runtime", "operator", "--managed-namespace", "installation-a", "installation-b", "readOnlyRootFilesystem: true", "allowPrivilegeEscalation: false"} {
		if !bytes.Contains(b, []byte(s)) {
			t.Fatal("missing", s)
		}
	}
	for _, s := range []string{"kind: StatefulSet", "kind: DaemonSet", "pods/exec", "mountPath: /persistent", "kind: Secret"} {
		if bytes.Contains(b, []byte(s)) {
			t.Fatal("unexpected authority or component workload", s)
		}
	}
}
func TestExplicitNamespacesRequired(t *testing.T) {
	if _, e := exec.LookPath("helm"); e != nil {
		t.Skip("Helm required")
	}
	if b, e := exec.Command("helm", "template", "fixture", ".").CombinedOutput(); e == nil || !bytes.Contains(b, []byte("managedNamespaces")) {
		t.Fatal("implicit namespace authority accepted")
	}
}
func TestPlacement(t *testing.T) {
	b := render(t, "--set", "placement.nodeSelector.pool=infra")
	if !bytes.Contains(b, []byte("pool: infra")) {
		t.Fatal("placement omitted")
	}
}
