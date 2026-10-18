package provision

import (
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"strings"
	"testing"
)

func TestDatabaseInstructions(t *testing.T) {
	for _, target := range []string{"native", "kubernetes"} {
		b := &v1.DatabaseConfiguration{Type: "postgres"}
		if target == "native" {
			b.UrlFile = "/etc/funnel/database.url"
		} else {
			b.ConnectionSecret = "cnpg-app"
		}
		p, e := Resolve(&v1.DeploymentRequest{Mode: "control", Target: target, ResourceName: "control", Context: "cluster", Database: b}, "", "v3.1.0")
		if e != nil {
			t.Fatal(e)
		}
		args := strings.Join(Arguments(p, "create"), " ")
		if !strings.Contains(args, "--database postgres") || !strings.Contains(args, "--database-tls-mode verify-full") {
			t.Fatal(args)
		}
		if strings.Contains(args, "password") {
			t.Fatal("credentials in instructions")
		}
		if target == "kubernetes" && !strings.Contains(args, "--database-secret cnpg-app") {
			t.Fatal(args)
		}
	}
	for _, d := range []*v1.DeploymentRequest{
		{Mode: "loadbalancer", Target: "native", Database: &v1.DatabaseConfiguration{Type: "postgres"}},
		{Mode: "control", Target: "kubernetes", Database: &v1.DatabaseConfiguration{Type: "postgres", UrlFile: "/etc/file"}},
		{Mode: "control", Target: "native", Database: &v1.DatabaseConfiguration{Type: "postgres", UrlFile: "relative"}},
		{Mode: "control", Target: "native", Database: &v1.DatabaseConfiguration{Type: "sqlite", UrlFile: "/etc/file"}},
	} {
		if _, e := Resolve(d, "", "v3.1.0"); e == nil {
			t.Fatal("invalid database settings accepted")
		}
	}
}
