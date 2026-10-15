package chart_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestSingleControllerAndForwarderPermissions(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("Helm is required for chart contract tests")
	}
	out, err := exec.Command("helm", "template", "fixture", ".", "--set", "bootstrap.existingSecret=fixture-bootstrap").CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	dec := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	deployment, daemonset, broker := false, false, false
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if len(raw) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		kind, _ := obj["kind"].(string)
		switch kind {
		case "StatefulSet":
			t.Fatal("gateway controller must not create a StatefulSet")
		case "Deployment":
			spec := obj["spec"].(map[string]any)
			if spec["replicas"] != float64(1) || spec["strategy"].(map[string]any)["type"] != "Recreate" {
				t.Fatal("controller must be singleton Recreate")
			}
			deployment = true
		case "DaemonSet":
			daemonset = true
		case "Service":
			if obj["metadata"].(map[string]any)["name"] == "fixture-broker" {
				selector := obj["spec"].(map[string]any)["selector"].(map[string]any)
				if _, ok := selector["tailscale-private-funnel.io/leader"]; ok {
					t.Fatal("broker still selects leader label")
				}
				broker = true
			}
		case "Role", "ClusterRole":
			for _, rule := range obj["rules"].([]any) {
				r := rule.(map[string]any)
				for _, resource := range r["resources"].([]any) {
					if resource != "pods" && resource != "pods/exec" {
						continue
					}
					for _, verb := range r["verbs"].([]any) {
						if verb != "get" && verb != "list" && verb != "watch" {
							t.Fatalf("unexpected Pod mutation permission: %v", r)
						}
					}
				}
			}
		}
	}
	if !deployment || !daemonset || !broker {
		t.Fatal("missing controller, forwarder, or broker")
	}
	if out, err = exec.Command("helm", "template", "fixture", ".", "--set", "bootstrap.existingSecret=fixture-bootstrap", "--set", "controller.replicas=2").CombinedOutput(); err == nil {
		t.Fatal("accepted concurrent controller replicas")
	} else if !bytes.Contains(out, []byte("exactly one replica")) {
		t.Fatalf("unexpected validation error: %s", out)
	}
}

func TestControlAndLoadBalancerStorageAndRBAC(t *testing.T) {
	if _, e := exec.LookPath("helm"); e != nil {
		t.Skip("Helm required")
	}
	for _, component := range []string{"control", "loadbalancer"} {
		out, e := exec.Command("helm", "template", "fixture", ".", "--set", "component="+component, "--set", "bootstrap.existingSecret=fixture").CombinedOutput()
		if e != nil {
			t.Fatal(e, string(out))
		}
		if bytes.Contains(out, []byte("kind: StatefulSet")) || bytes.Contains(out, []byte("volumeClaimTemplates")) {
			t.Fatal("old workload contract")
		}
		for _, expected := range []string{"kind: Deployment", "strategy: {type: Recreate}", "mountPath: /persistent", "name: runtime, emptyDir", "helm.sh/resource-policy: keep", "resourceNames: [\"fixture-identity\"]", "verbs: [get, update, patch]"} {
			if !bytes.Contains(out, []byte(expected)) {
				t.Fatal(component, expected)
			}
		}
		if bytes.Contains(out, []byte("kind: Secret\n")) {
			t.Fatal("identity data must not enter Helm release metadata")
		}
	}
}
