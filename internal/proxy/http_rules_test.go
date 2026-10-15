package proxy

import "testing"

func TestHTTPRuleRejectsUntrustedConfiguration(t *testing.T) {
	r := HTTPRule{Backends: []Backend{{Key: "backend", Address: "10.43.0.1:8080", Port: 8080, Weight: 1}}, Matches: []Match{{Path: "/api"}}}
	if err := ValidateHTTPRule(r, false); err != nil {
		t.Fatal(err)
	}
	r.RequestHeaders.Set = map[string]string{"X-Forwarded-For": "spoof"}
	if ValidateHTTPRule(r, false) == nil {
		t.Fatal("spoof allowed")
	}
	r.RequestHeaders.Set = nil
	r.Backends[0].Address = "127.0.0.1:8080"
	if ValidateHTTPRule(r, false) == nil {
		t.Fatal("loopback allowed")
	}
}
