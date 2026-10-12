package policy

import "testing"

func TestEmptyPolicyDenies(t *testing.T) {
	if ok, _ := (Document{}).Allows("app.example.com", "gw.example.com", []string{"ingress-1"}, []string{"tag:ingress"}); ok {
		t.Fatal("empty policy must deny exposure")
	}
}

func TestRuleMatchesFullHostnameAndRole(t *testing.T) {
	d := Document{Rules: []Rule{{Name: "dev", HostnameRegexes: []string{`[^.]+\.dev\.example\.com`}, Roles: []string{"group:platform"}, LoadBalancerIDs: []string{"loadbalancer.example.com"}, Enabled: true}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d.Allows("api.dev.example.com.", "loadbalancer.example.com", []string{"ingress-1"}, []string{"group:platform"}); !ok {
		t.Fatal("matching role and loadbalancer should be allowed")
	}
	if ok, _ := d.Allows("api.prod.example.com", "loadbalancer.example.com", []string{"ingress-1"}, []string{"group:platform"}); ok {
		t.Fatal("hostname outside the rule should be denied")
	}
}

func TestOptionalPolicyRestrictions(t *testing.T) {
	tests := []struct {
		name           string
		rule           Rule
		ingress, roles []string
		lb             string
		want           bool
	}{
		{"no restrictions", Rule{}, nil, nil, "", true},
		{"unrestricted enrolled node", Rule{}, []string{"other"}, nil, "edge.example.com", true},
		{"ingress mismatch", Rule{IngressIDs: []string{"one"}}, []string{"two"}, nil, "", false},
		{"ingress match", Rule{IngressIDs: []string{"one"}}, []string{"one"}, nil, "", true},
		{"role mismatch", Rule{Roles: []string{"tag:allowed"}}, []string{"any"}, []string{"tag:other"}, "", false},
		{"role match", Rule{Roles: []string{"tag:allowed"}}, nil, []string{"tag:allowed"}, "", true},
		{"load balancer mismatch", Rule{LoadBalancerIDs: []string{"edge.example.com"}}, nil, nil, "other.example.com", false},
		{"load balancer required when restricted", Rule{LoadBalancerIDs: []string{"edge.example.com"}}, nil, nil, "", false},
		{"load balancer match", Rule{LoadBalancerIDs: []string{"edge.example.com"}}, nil, nil, "edge.example.com", true},
		{"blank selectors", Rule{Roles: []string{" "}, IngressIDs: []string{" "}, LoadBalancerIDs: []string{" "}}, nil, nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.rule.HostnameRegexes = []string{`app[.]example[.]com`}
			tt.rule.Enabled = true
			d := Document{Rules: []Rule{tt.rule}}
			if err := d.Validate(); err != nil {
				t.Fatal(err)
			}
			if got, _ := d.Allows("app.example.com", tt.lb, tt.ingress, tt.roles); got != tt.want {
				t.Fatal(got, tt.want)
			}
			if got, _ := d.Allows("other.example.com", tt.lb, tt.ingress, tt.roles); got {
				t.Fatal("hostname constraint bypassed")
			}
			d.Rules[0].Enabled = false
			if got, _ := d.Allows("app.example.com", tt.lb, tt.ingress, tt.roles); got {
				t.Fatal("disabled rule allowed")
			}
		})
	}
}

func TestRuleRejectsNonDNSHostnameAtMatch(t *testing.T) {
	d := Document{Rules: []Rule{{HostnameRegexes: []string{`.*`}, IngressIDs: []string{"ingress-1"}, Enabled: true}}}
	if ok, _ := d.Allows("*.example.com", "loadbalancer.example.com", []string{"ingress-1"}, nil); ok {
		t.Fatal("wildcard hostname must not be auto-provisioned")
	}
	if ok, _ := d.Allows("192.0.2.1", "loadbalancer.example.com", []string{"ingress-1"}, nil); ok {
		t.Fatal("IP address must not be auto-provisioned as a DNS name")
	}
}

func TestMultipleHostnamePatterns(t *testing.T) {
	d := Document{Rules: []Rule{{HostnameRegexes: []string{`api[.]example[.]com`, `app[0-9]{1,3}[.]example[.]net`}, Enabled: true}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.example.com", "app12.example.net"} {
		if ok, _ := d.Allows(host, "", nil, nil); !ok {
			t.Fatal("pattern did not match", host)
		}
	}
	for _, host := range []string{"evilapi.example.com", "app1234.example.net", "api.example.net"} {
		if ok, _ := d.Allows(host, "", nil, nil); ok {
			t.Fatal("unexpected match", host)
		}
	}
	d.Rules[0].HostnameRegexes = append(d.Rules[0].HostnameRegexes, "[")
	if err := d.Validate(); err == nil {
		t.Fatal("invalid additional pattern accepted")
	}
	if ok, _ := d.Allows("api.example.com", "", nil, nil); ok {
		t.Fatal("invalid rule did not fail closed")
	}
	for _, patterns := range [][]string{nil, {}, {" "}, {"valid", ""}} {
		d.Rules[0].HostnameRegexes = patterns
		if err := d.Validate(); err == nil {
			t.Fatal("missing/empty pattern accepted", patterns)
		}
	}
}

func TestLoadBalancerIDsAreCaseSensitive(t *testing.T) {
	d := Document{Rules: []Rule{{Name: "test", HostnameRegexes: []string{".*"}, LoadBalancerIDs: []string{"Ab_Cd"}, Enabled: true}}}
	if ok, _ := d.Allows("app.example.com", "ab_cd", nil, nil); ok {
		t.Fatal("ID case ignored")
	}
	if ok, _ := d.Allows("app.example.com", "Ab_Cd", nil, nil); !ok {
		t.Fatal("exact ID denied")
	}
}

func TestDirectRouteCannotUseIngressOnlyPolicy(t *testing.T) {
	doc := Document{Rules: []Rule{{Name: "ingress only", HostnameRegexes: []string{`voice[.]example[.]com`}, IngressIDs: []string{"ingress-1"}, Enabled: true}}}
	if ok, _ := doc.Allows("voice.example.com", "edge-1", nil, nil); ok {
		t.Fatal("direct route was authorized by an ingress-only rule")
	}
	doc.Rules = append(doc.Rules, Rule{Name: "load balancer role", HostnameRegexes: []string{`voice[.]example[.]com`}, LoadBalancerIDs: []string{"edge-1"}, Roles: []string{"tag:backend-access"}, Enabled: true})
	if ok, _ := doc.Allows("voice.example.com", "edge-1", nil, []string{"tag:backend-access"}); !ok {
		t.Fatal("load balancer role rule did not authorize direct route")
	}
}
