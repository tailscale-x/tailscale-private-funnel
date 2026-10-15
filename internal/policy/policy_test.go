package policy

import "testing"

func TestEmptyPolicyDenies(t *testing.T) {
	if ok, _ := (Document{}).Allows("app.example.com", []string{"gateway-1"}, []string{"tag:gateway"}); ok {
		t.Fatal("empty policy must deny exposure")
	}
}

func TestRuleRejectsNonDNSHostnameAtMatch(t *testing.T) {
	d := Document{Rules: []Rule{{HostnameRegexes: []string{`.*`}, GatewayIDs: []string{"gateway-1"}, Enabled: true}}}
	if ok, _ := d.Allows("*.example.com", []string{"gateway-1"}, nil); ok {
		t.Fatal("wildcard hostname must not be auto-provisioned")
	}
	if ok, _ := d.Allows("192.0.2.1", []string{"gateway-1"}, nil); ok {
		t.Fatal("IP address must not be auto-provisioned as a DNS name")
	}
}

func TestMultipleHostnamePatterns(t *testing.T) {
	d := Document{Rules: []Rule{{HostnameRegexes: []string{`api[.]example[.]com`, `app[0-9]{1,3}[.]example[.]net`}, Enabled: true}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.example.com", "app12.example.net"} {
		if ok, _ := d.Allows(host, nil, nil); !ok {
			t.Fatal("pattern did not match", host)
		}
	}
	for _, host := range []string{"evilapi.example.com", "app1234.example.net", "api.example.net"} {
		if ok, _ := d.Allows(host, nil, nil); ok {
			t.Fatal("unexpected match", host)
		}
	}
	d.Rules[0].HostnameRegexes = append(d.Rules[0].HostnameRegexes, "[")
	if err := d.Validate(); err == nil {
		t.Fatal("invalid additional pattern accepted")
	}
	if ok, _ := d.Allows("api.example.com", nil, nil); ok {
		t.Fatal("invalid rule did not fail closed")
	}
	for _, patterns := range [][]string{nil, {}, {" "}, {"valid", ""}} {
		d.Rules[0].HostnameRegexes = patterns
		if err := d.Validate(); err == nil {
			t.Fatal("missing/empty pattern accepted", patterns)
		}
	}
}
