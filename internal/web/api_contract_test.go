package web

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestGeneratedOpenAPIContainsEveryCurrentRPC(t *testing.T) {
	documents := map[string]string{"ManagementService": "openapi.json", "DeploymentService": "openapi.json", "NodeService": "node-openapi.json", "BrokerService": "broker-openapi.json", "WorkloadBootstrapService": "bootstrap-openapi.json"}
	for _, file := range []protoreflect.FileDescriptor{v1.File_funnel_v1_management_proto, v1.File_funnel_v1_provisioning_proto, v1.File_funnel_v1_runtime_proto} {
		for i := 0; i < file.Services().Len(); i++ {
			service := file.Services().Get(i)
			name, ok := documents[string(service.Name())]
			if !ok {
				t.Fatalf("undocumented service %s", service.FullName())
			}
			b, e := Assets.ReadFile("assets/api/" + name)
			if e != nil {
				t.Fatal(e)
			}
			var doc struct {
				Paths map[string]json.RawMessage `json:"paths"`
			}
			if e = json.Unmarshal(b, &doc); e != nil {
				t.Fatal(e)
			}
			for j := 0; j < service.Methods().Len(); j++ {
				path := "/" + string(service.FullName()) + "/" + string(service.Methods().Get(j).Name())
				if _, ok := doc.Paths[path]; !ok {
					t.Fatalf("missing generated RPC %s from %s", path, name)
				}
			}
		}
	}
	b, e := Assets.ReadFile("assets/api/api.md")
	if e != nil {
		t.Fatal(e)
	}
	for _, obsolete := range []string{"Enrollment additionally requires", "Signed payload bytes", "RevokeEnrollment"} {
		if strings.Contains(string(b), obsolete) {
			t.Fatalf("obsolete API instruction %q", obsolete)
		}
	}
}
