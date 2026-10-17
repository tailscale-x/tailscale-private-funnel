package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func rpcFixture(t *testing.T, p Principal) (*Control, funnelv1connect.ManagementServiceClient, funnelv1connect.DeploymentServiceClient) {
	t.Helper()
	s, e := store.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "viewer": {"node:viewer"}, "operator": {"node:operator"}}}); e != nil {
		t.Fatal(e)
	}
	if e = s.SetJSON("tailnet", map[string]string{"management_url": "http://control.example.ts.net:8080", "release_version": "v2.0.0"}); e != nil {
		t.Fatal(e)
	}
	c := &Control{Store: s, BuildVersion: "test", rolesExpire: time.Now().Add(time.Hour)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.Handler().ServeHTTP(w, WithPrincipal(r, p)) }))
	t.Cleanup(server.Close)
	return c, funnelv1connect.NewManagementServiceClient(server.Client(), server.URL), funnelv1connect.NewDeploymentServiceClient(server.Client(), server.URL)
}
func TestRPCMetadataRoleEnforcement(t *testing.T) {
	for _, tc := range []struct {
		id          string
		read, write bool
	}{{"admin", true, true}, {"viewer", true, false}, {"operator", true, false}, {"unknown", false, false}} {
		t.Run(tc.id, func(t *testing.T) {
			_, client, _ := rpcFixture(t, Principal{NodeID: tc.id, Tagged: true})
			_, e := client.Health(context.Background(), connect.NewRequest(&v1.Empty{}))
			if (e == nil) != tc.read {
				t.Fatalf("read error=%v", e)
			}
			_, e = client.ValidatePolicy(context.Background(), connect.NewRequest(&v1.Policy{}))
			if (e == nil) != tc.write {
				t.Fatalf("write error=%v", e)
			}
		})
	}
}
func TestRPCProviderRenameKeepsCredentialsAndImmutableID(t *testing.T) {
	c, client, _ := rpcFixture(t, Principal{NodeID: "admin"})
	p, e := client.SaveProvider(t.Context(), connect.NewRequest(&v1.SaveProviderRequest{Name: "Original", Vendor: "cloudflare", AccountId: "account", Credentials: map[string]string{"api_token": "secret-fixture"}}))
	if e != nil {
		t.Fatal(e)
	}
	if p.Msg.Id == "Original" || p.Msg.Id == "" {
		t.Fatal("ID not generated")
	}
	q, e := client.SaveProvider(t.Context(), connect.NewRequest(&v1.SaveProviderRequest{Id: p.Msg.Id, Name: "新しい名前", Credentials: map[string]string{"api_token": ""}}))
	if e != nil {
		t.Fatal(e)
	}
	_, config, e := c.Store.ProviderConfig(q.Msg.Id)
	if e != nil || q.Msg.Id != p.Msg.Id || config["api_token"] != "secret-fixture" {
		t.Fatalf("rename lost data: %v", e)
	}
	if q.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("credential mutation cacheable")
	}
	if strings.Contains(q.Msg.String(), "secret-fixture") {
		t.Fatal("credential exposed")
	}
	for _, field := range []string{"credentials", "api_token"} {
		if q.Msg.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field)) != nil {
			t.Fatal("response has secret field")
		}
	}
}
func TestRPCValidationAndPreviewAreSideEffectFree(t *testing.T) {
	c, client, provisioning := rpcFixture(t, Principal{NodeID: "admin"})
	_, e := client.GetNode(t.Context(), connect.NewRequest(&v1.ResourceID{}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatalf("validation=%v", e)
	}
	before, _ := c.Store.Enrollments("")
	result, e := provisioning.Instructions(t.Context(), connect.NewRequest(&v1.DeploymentRequest{Mode: "tailnet-gateway", Target: "native", Hostname: "test-node"}))
	if e != nil {
		t.Fatal(e)
	}
	after, _ := c.Store.Enrollments("")
	if len(before) != len(after) || result.Msg.Credentials != nil {
		t.Fatal("preview minted credentials")
	}
}
func TestEveryManagementMethodHasPermissionAndCLI(t *testing.T) {
	services := v1.File_funnel_v1_management_proto.Services()
	for i := 0; i < services.Len(); i++ {
		methods := services.Get(i).Methods()
		for j := 0; j < methods.Len(); j++ {
			m := methods.Get(j)
			permission, _ := proto.GetExtension(m.Options(), v1.E_Permission).(v1.Permission)
			command, _ := proto.GetExtension(m.Options(), v1.E_Cli).(string)
			if permission == v1.Permission_PERMISSION_UNSPECIFIED || command == "" {
				t.Fatalf("missing metadata: %s", m.FullName())
			}
		}
	}
}

func TestEveryRPCMutationEnforcesDescriptorPermission(t *testing.T) {
	for _, role := range []string{"viewer", "operator"} {
		t.Run(role, func(t *testing.T) {
			c, _, _ := rpcFixture(t, Principal{NodeID: role, Tagged: true})
			server := rpcServer(t, c, Principal{NodeID: role, Tagged: true})
			service := v1.File_funnel_v1_management_proto.Services().ByName("ManagementService")
			for i := 0; i < service.Methods().Len(); i++ {
				method := service.Methods().Get(i)
				permission, _ := proto.GetExtension(method.Options(), v1.E_Permission).(v1.Permission)
				denied := permission == v1.Permission_PERMISSION_ADMIN || (role == "viewer" && permission == v1.Permission_PERMISSION_OPERATOR)
				if !denied {
					continue
				}
				request, _ := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/"+string(service.FullName())+"/"+string(method.Name()), strings.NewReader(`{}`))
				request.Header.Set("Content-Type", "application/json")
				result, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				result.Body.Close()
				if result.StatusCode != 403 {
					t.Fatalf("%s allowed %s: %d", method.Name(), role, result.StatusCode)
				}
			}
		})
	}
}

func TestDeploymentInstructionsReadOnlyAndAdminOnly(t *testing.T) {
	for _, role := range []string{"admin", "operator", "viewer"} {
		t.Run(role, func(t *testing.T) {
			c, _, client := rpcFixture(t, Principal{NodeID: role, Tagged: true})
			before, _ := c.Store.Enrollments("")
			r, e := client.Instructions(t.Context(), connect.NewRequest(&v1.DeploymentRequest{Mode: "docker-gateway", Target: "native", ResourceName: "site", DisplayName: "Site", Version: "dev-test"}))
			if role != "admin" {
				if connect.CodeOf(e) != connect.CodePermissionDenied {
					t.Fatal(e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			after, _ := c.Store.Enrollments("")
			if len(after) != len(before) || r.Msg.Credentials != nil {
				t.Fatal("instruction generation created enrollment")
			}
			if !strings.Contains(r.Msg.Commands["create"], "'native' 'gateways' 'create'") {
				t.Fatal("incorrect CLI renderer")
			}
		})
	}
}
func TestDeploymentNodeSelectorResolvesServerMetadata(t *testing.T) {
	c, _, client := rpcFixture(t, Principal{NodeID: "admin", Tagged: true})
	c.Store.UpsertNode("node", "tailnet-gateway", "site")
	c.Store.SetJSON("node_deployment:node", nodeDeployment{DeploymentTarget: "native", ResourceName: "handle", Version: "dev-test"})
	r, e := client.Instructions(t.Context(), connect.NewRequest(&v1.DeploymentRequest{NodeId: "node"}))
	if e != nil {
		t.Fatal(e)
	}
	if r.Msg.Deployment.ResourceName != "handle" {
		t.Fatal("client selector not resolved")
	}
}

func TestResourceUpdateInstructionsUseSelectedReleaseInsteadOfEnrollmentVersion(t *testing.T) {
	c, _, client := rpcFixture(t, Principal{NodeID: "admin", Tagged: true})
	c.Store.UpsertNode("node", "tailnet-gateway", "site")
	c.Store.SetJSON("node_deployment:node", nodeDeployment{DeploymentTarget: "native", ResourceName: "site", Version: "v2.0.0"})
	c.Store.SetJSON("tailnet", map[string]string{"release_version": "v3.0.0", "management_url": "http://control.example.ts.net:8080"})
	for _, version := range []string{"", "v3.1.0"} {
		r, e := client.Instructions(t.Context(), connect.NewRequest(&v1.DeploymentRequest{NodeId: "node", Version: version}))
		if e != nil {
			t.Fatal(e)
		}
		want := version
		if want == "" {
			want = "v3.0.0"
		}
		if r.Msg.Version != want || !strings.Contains(r.Msg.Commands["update"], want) || strings.Contains(r.Msg.Commands["update"], "v2.0.0") {
			t.Fatal("stale or ignored update version", r.Msg.Version, r.Msg.Commands["update"])
		}
	}
}
