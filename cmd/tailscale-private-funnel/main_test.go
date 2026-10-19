package main

import (
	"bytes"
	"strings"
	"testing"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/operator"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPublicInventory(t *testing.T) {
	for _, scope := range []string{"native", "k8s"} {
		for _, resource := range []string{"controls", "gateways", "loadbalancers"} {
			actions := []string{"create", "list", "get", "update", "delete"}
			if scope == "native" {
				actions = append(actions, "start", "stop", "restart", "logs")
			}
			for _, action := range actions {
				cmd, rest, e := NewRoot("dev-test").Find([]string{scope, resource, action})
				if e != nil || cmd.Name() != action || len(rest) > 0 {
					t.Fatalf("missing %s %s %s", scope, resource, action)
				}
			}
		}
	}
	for _, name := range []string{"whoami", "health", "providers", "zones", "policies", "nodes", "exposures", "applications", "reports", "services", "settings", "access"} {
		c, _, e := NewRoot("dev-test").Find([]string{"manage", name})
		if e != nil || c.Name() != name {
			t.Fatal("missing management", name)
		}
	}
}
func TestRemovedCommandsRejected(t *testing.T) {
	for _, args := range [][]string{{"native", "gateways", "create-remote", "site"}, {"k8s", "gateways", "create-remote", "site"}, {"native", "gateways", "create", "site", "--bootstrap-stdin"}, {"control", "join"}, {"control", "providers"}, {"gateway", "serve"}, {"ingress", "join"}, {"k8s-gateway", "serve"}, {"k8s-operator", "serve"}, {"native", "gateways", "join"}, {"manage", "provision"}} {
		r := NewRoot("dev-test")
		r.SetArgs(args)
		r.SetOut(&bytes.Buffer{})
		r.SetErr(&bytes.Buffer{})
		if e := r.Execute(); e == nil {
			t.Fatal("accepted old command", args)
		}
	}
}
func TestKubernetesContextRequired(t *testing.T) {
	r := NewRoot("dev-test")
	r.SetArgs([]string{"k8s", "gateways", "list"})
	if e := r.Execute(); e == nil || !strings.Contains(e.Error(), "--context") {
		t.Fatal(e)
	}
}
func TestRemoteDryRunDoesNotAuthorize(t *testing.T) {
	r := NewRoot("dev-test")
	r.SetArgs([]string{"native", "gateways", "create", "sample", "--type", "tailnet", "--control-url", "http://unreachable.invalid:8080", "--dry-run", "--output", "script"})
	var b bytes.Buffer
	r.SetOut(&b)
	if e := r.Execute(); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(b.String(), "exit 1") || strings.Contains(b.String(), "auth-key") {
		t.Fatal(b.String())
	}
}

func TestRenderedRuntimeArguments(t *testing.T) {
	spec := funnel.ComponentSpec{Image: "ghcr.io/tailscale-x/tailscale-private-funnel:dev-test", Installation: "test", Hostname: "test"}
	meta := metav1.ObjectMeta{Name: "test", Namespace: "funnel-test", UID: "uid"}
	for _, o := range []client.Object{&funnel.FunnelControl{ObjectMeta: meta, Spec: funnel.ControlSpec{ComponentSpec: spec, Tailnet: "example.ts.net"}}, &funnel.FunnelLoadBalancer{ObjectMeta: meta, Spec: funnel.LoadBalancerSpec{ComponentSpec: spec}}, &funnel.FunnelGateway{ObjectMeta: meta, Spec: funnel.GatewaySpec{ComponentSpec: spec, GatewayClass: "test"}}} {
		objects, e := operator.Render(o)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range objects {
			u := v.(*unstructured.Unstructured)
			for _, field := range []string{"containers", "initContainers"} {
				containers, _, _ := unstructured.NestedSlice(u.Object, "spec", "template", "spec", field)
				for _, raw := range containers {
					c := raw.(map[string]any)
					args := []string{}
					for _, arg := range c["args"].([]any) {
						args = append(args, arg.(string))
					}
					args = append(args, "--help")
					r := NewRoot("dev-test")
					r.SetOut(&bytes.Buffer{})
					r.SetErr(&bytes.Buffer{})
					r.SetArgs(args)
					if e = r.Execute(); e != nil {
						t.Fatalf("invalid generated runtime arguments %v: %v", args, e)
					}
				}
			}
		}
	}
}

func TestGeneratedPublicCommandsParse(t *testing.T) {
	for _, mode := range []string{"control", "loadbalancer", "docker-gateway", "tailnet-gateway", "k8s-gateway", "operator"} {
		targets := []string{"native"}
		if mode == "control" || mode == "loadbalancer" {
			targets = append(targets, "kubernetes")
		}
		if mode == "k8s-gateway" || mode == "operator" {
			targets = []string{"kubernetes"}
		}
		for _, target := range targets {
			in := &v1.DeploymentRequest{Mode: mode, Target: target, ResourceName: "site", Context: "explicit", Installation: "team", ManagedNamespaces: []string{"funnel-team"}}
			if mode == "loadbalancer" {
				in.PublicHostname = "edge.example.com"
				in.CertificateEmail = "admin@example.com"
			}
			p, e := provision.Resolve(in, "http://control:8080", "dev-test")
			if e != nil {
				t.Fatal(e)
			}
			args := provision.Arguments(p, "create")[1:]
			c, rest, e := NewRoot("dev-test").Find(args)
			if e != nil {
				t.Fatal(e)
			}
			if e = c.ParseFlags(rest); e != nil {
				t.Fatalf("invalid rendered command %v: %v", args, e)
			}
		}
	}
}
