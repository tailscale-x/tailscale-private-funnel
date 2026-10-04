package dns

import "testing"

func TestIngressRecordsOnlyOwnedPublicAlias(t *testing.T) {
	recs, err := IngressRecords(IngressExposure{PublicHostname: "app.example", LoadBalancerHostname: "loadbalancer.example"})
	if err != nil || len(recs) != 1 {
		t.Fatalf("records=%v err=%v", recs, err)
	}
	if got := recs[0].RR().Data; got != "loadbalancer.example" {
		t.Fatalf("cname=%q", got)
	}
}

func TestLoadBalancerAmbiguousEndpoint(t *testing.T) {
	if _, preserve, err := LoadBalancerRecord(LoadBalancerEndpoint{Hostname: "loadbalancer.example", PublicIPv4: []string{"1.2.3.4", "5.6.7.8"}}); err == nil || !preserve {
		t.Fatalf("expected preserve error, got err=%v preserve=%v", err, preserve)
	}
}

func TestLoadBalancerEndpointDeduplicatesPorts(t *testing.T) {
	record, preserve, err := LoadBalancerRecord(LoadBalancerEndpoint{Hostname: "loadbalancer.example", PublicIPv4: []string{"198.51.100.10:443", "198.51.100.10:8443"}})
	if err != nil || preserve {
		t.Fatalf("record=%v preserve=%v err=%v", record, preserve, err)
	}
	if got := record.RR().Data; got != "198.51.100.10" {
		t.Fatalf("data=%q", got)
	}
}

func TestLoadBalancerPrivateEndpointIsRetained(t *testing.T) {
	if _, preserve, err := LoadBalancerRecord(LoadBalancerEndpoint{Hostname: "loadbalancer.example", PublicIPv4: []string{"100.64.0.10:443"}}); err == nil || !preserve {
		t.Fatalf("expected retained endpoint error, got err=%v preserve=%v", err, preserve)
	}
}
