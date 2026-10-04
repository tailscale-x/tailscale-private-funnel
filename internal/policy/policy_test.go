package policy

import "testing"

func TestEmptyPolicyDenies(t *testing.T) {
	if ok, _ := (Document{}).Allows("app.example.com", "gw.example.com", []string{"ingress-1"}, []string{"tag:ingress"}); ok {
		t.Fatal("empty policy must deny exposure")
	}
}

func TestRuleMatchesFullHostnameAndRole(t *testing.T) {
	d := Document{Rules: []Rule{{Name: "dev", HostnameRegex: `[^.]+\.dev\.example\.com`, Roles: []string{"group:platform"}, LoadBalancerHostnames: []string{"loadbalancer.example.com"}, Enabled: true}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.Allows("api.dev.example.com.", "loadbalancer.example.com.", []string{"ingress-1"}, []string{"group:platform"}); !ok {
		t.Fatal("matching role and loadbalancer should be allowed")
	}
	if ok, _ := d.Allows("api.prod.example.com", "loadbalancer.example.com", []string{"ingress-1"}, []string{"group:platform"}); ok {
		t.Fatal("hostname outside the rule should be denied")
	}
}

func TestRuleRequiresPrincipalSelector(t *testing.T) {
	if err := (Document{Rules: []Rule{{HostnameRegex: `.*`, Enabled: true}}}).Validate(); err == nil {
		t.Fatal("a rule without role or ingress selector must be rejected")
	}
}

func TestRuleRejectsNonDNSHostnameAtMatch(t *testing.T) {
	d := Document{Rules: []Rule{{HostnameRegex: `.*`, IngressIDs: []string{"ingress-1"}, Enabled: true}}}
	if ok, _ := d.Allows("*.example.com", "loadbalancer.example.com", []string{"ingress-1"}, nil); ok {
		t.Fatal("wildcard hostname must not be auto-provisioned")
	}
	if ok, _ := d.Allows("192.0.2.1", "loadbalancer.example.com", []string{"ingress-1"}, nil); ok {
		t.Fatal("IP address must not be auto-provisioned as a DNS name")
	}
}
