package identity

import (
	"slices"
	"testing"
)

func TestRoleIsolation(t *testing.T) {
	for _, role := range Roles {
		if e := Authorize("main", role, []string{Tag("main", role)}); e != nil {
			t.Fatal(e)
		}
		if e := Authorize("main", role, []string{Tag("other", role)}); e == nil {
			t.Fatal("cross-installation role authorized")
		}
		for _, other := range Roles {
			if other != role && Authorize("main", role, []string{Tag("main", other)}) == nil {
				t.Fatal("wrong role authorized")
			}
		}
	}
}
func TestPolicySeparatesControlAndApplications(t *testing.T) {
	p, e := PolicyTemplate("main", 8080)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.TagOwners) != 8 || len(p.Grants) != 4 {
		t.Fatal(p)
	}
	issuer := IssuerTag("main")
	for _, role := range Roles {
		if !slices.Contains(p.TagOwners[Tag("main", role)], issuer) {
			t.Fatal("issuer lacks direct role ownership", role)
		}
		if Authorize("main", role, []string{issuer}) == nil {
			t.Fatal("issuer tag gained runtime authority")
		}
	}
	for _, owners := range [][]string{p.TagOwners[ServiceTag("main", true)], p.TagOwners[ServiceTag("main", false)]} {
		if slices.Contains(owners, issuer) {
			t.Fatal("issuer owns Service tags")
		}
	}
	for _, hosts := range p.AutoApprovers.Services {
		if slices.Contains(hosts, issuer) {
			t.Fatal("issuer may advertise Services")
		}
	}
	for _, g := range p.Grants {
		if slices.Contains(g.Src, issuer) || slices.Contains(g.Dst, issuer) {
			t.Fatal("issuer received network access")
		}
		if slices.Contains(g.Dst, ServiceTag("main", false)) && (len(g.Src) != 1 || g.Src[0] != Tag("main", LoadBalancer)) {
			t.Fatal("application access broadened")
		}
	}
	if slices.Contains(p.AutoApprovers.Services[ServiceTag("main", false)], Tag("main", LoadBalancer)) {
		t.Fatal("load balancer may advertise application Services")
	}
	for _, bad := range []string{"", "A", "../main", "a-b-", "abcdefghijklmnopqrstuvwxyz"} {
		if _, e := PolicyTemplate(bad, 8080); e == nil {
			t.Fatal(bad)
		}
	}
}

func TestServiceManagementOwnershipIsSeparateFromKeyIssuance(t *testing.T) {
	p, err := PolicyTemplate("main", 8080)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{ServiceTag("main", true), ServiceTag("main", false)} {
		if !slices.Contains(p.TagOwners[tag], Tag("main", Control)) {
			t.Fatal("Service-management client cannot assign Service tag", tag)
		}
		if slices.Contains(p.TagOwners[tag], IssuerTag("main")) {
			t.Fatal("auth-key issuer gained Service ownership")
		}
	}
}

func TestServicePeerDiscoveryDoesNotGrantBackendPorts(t *testing.T) {
	for _, installation := range []string{"main", "isolated-site"} {
		p, err := PolicyTemplate(installation, 8080)
		if err != nil {
			t.Fatal(err)
		}
		deviceGrants := 0
		for _, g := range p.Grants {
			for _, dst := range g.Dst {
				for _, role := range Roles {
					if dst != Tag(installation, role) {
						continue
					}
					deviceGrants++
					expectedSources := []string{Tag(installation, LoadBalancer)}
					if role == Control {
						expectedSources = []string{Tag(installation, LoadBalancer), Tag(installation, DockerGateway), Tag(installation, TailnetGateway), Tag(installation, KubernetesGateway)}
					}
					if !slices.Equal(g.Src, expectedSources) || !slices.Equal(g.IP, []string{"icmp:*"}) {
						t.Fatalf("device application access broadened: %#v", g)
					}
					if role != Control && role != DockerGateway && role != TailnetGateway && role != KubernetesGateway {
						t.Fatal("unexpected discovery destination", role)
					}
				}
			}
		}
		if deviceGrants != 4 {
			t.Fatalf("discovery destinations: %d", deviceGrants)
		}
	}
}
