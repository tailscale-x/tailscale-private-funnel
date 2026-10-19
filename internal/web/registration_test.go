package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
)

func TestRoleRegistrationAndManagementSeparation(t *testing.T) {
	for _, which := range []string{"correct", "wrong-role", "other-installation", "untagged"} {
		t.Run(which, func(t *testing.T) {
			c, _ := testControl(t)
			tags := []string{identity.Tag("main", identity.DockerGateway)}
			switch which {
			case "wrong-role":
				tags = []string{identity.Tag("main", identity.LoadBalancer)}
			case "other-installation":
				tags = []string{identity.Tag("other", identity.DockerGateway)}
			case "untagged":
				tags = nil
			}
			p := Principal{NodeID: "device", Hostname: "docker", Tagged: len(tags) > 0, Tags: tags}
			s := rpcServer(t, c, p)
			rpc := funnelv1connect.NewNodeServiceClient(s.Client(), s.URL)
			req := connect.NewRequest(&v1.RegisterComponentRequest{ComponentId: "docker", InstanceId: "process", Deployment: &v1.DeploymentRequest{Mode: "docker-gateway", Installation: "main", Name: "Docker", Hostname: "untrusted-hostname", Target: "native", TailscaleConnection: "host"}})
			response, e := rpc.RegisterComponent(t.Context(), req)
			if which != "correct" {
				if connect.CodeOf(e) != connect.CodePermissionDenied {
					t.Fatal("wrong role authorized", e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if response.Msg.DeviceId != "device" {
				t.Fatal(response.Msg)
			}
			n, e := c.Store.Node("docker")
			if e != nil || n.Hostname != "docker" {
				t.Fatal("unverified hostname accepted", n, e)
			}
			if err := c.Store.RenameResource("node", "docker", "My Docker gateway"); err != nil {
				t.Fatal(err)
			}
			if _, err := rpc.RegisterComponent(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			if renamed, err := c.Store.Node("docker"); err != nil || renamed.Name != "My Docker gateway" {
				t.Fatal("runtime reset a management display name", renamed, err)
			}
			// Runtime tags cannot obtain management authority, even if listed as an admin selector.
			_ = c.Store.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": tags}})
			_, e = funnelv1connect.NewManagementServiceClient(s.Client(), s.URL).ListNodes(t.Context(), connect.NewRequest(&v1.ListNodesRequest{}))
			if connect.CodeOf(e) != connect.CodePermissionDenied {
				t.Fatal("runtime role gained human management authority", e)
			}
			// Caller metadata selects a logical assignment; WhoIs still binds its device.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Header.Set("Funnel-Component-ID", "docker")
				r.Header.Set("Funnel-Instance-ID", "process")
				c.controlHandler().ServeHTTP(w, WithPrincipal(r, p))
			}))
			defer server.Close()
			client := funnelv1connect.NewNodeServiceClient(server.Client(), server.URL)
			if _, e = client.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{})); e != nil {
				t.Fatal(e)
			}
			p.NodeID = "spoofed"
			if _, e = client.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{})); connect.CodeOf(e) != connect.CodePermissionDenied {
				t.Fatal("device spoof accepted", e)
			}
		})
	}
}

func TestInstructionsBindCurrentInstallation(t *testing.T) {
	c, _ := testControl(t)
	c.Installation = "site-b"
	if err := c.Store.SetJSON("tailnet", map[string]string{"management_url": "http://control.example.ts.net:8080"}); err != nil {
		t.Fatal(err)
	}
	request := &v1.DeploymentRequest{Mode: "docker-gateway", Target: "native", ResourceName: "gateway", TailscaleConnection: "host"}
	plan, err := c.deploymentPlan(request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Deployment.Installation != "site-b" || plan.Namespace != "tailscale-private-funnel-site-b-system" {
		t.Fatal("installation default drift", plan.Deployment.Installation, plan.Namespace)
	}
	request.Installation = "other"
	if _, err = c.deploymentPlan(request); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("foreign installation accepted", err)
	}
}
