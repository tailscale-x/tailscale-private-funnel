package proxy

import "testing"

func TestPrefixRewriteValidation(t *testing.T) {
	backend := []Backend{{Key: "echo", Service: "100.64.0.1", Port: 8080, Weight: 1}}
	for _, path := range []string{"/", "/api", "/api/v1/", "/café"} {
		r := HTTPRule{Matches: []Match{{Path: path}}, Rewrite: &PrefixRewrite{ReplacePrefix: "/"}, Backends: backend}
		if err := ValidateHTTPRule(r, true); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{"", "relative", "/a?x=1", "/a#frag", "/{path}", "/$1", "/a*", "/a\x00", "/a\t", "/%xx"} {
		if err := ValidatePath(path); err == nil {
			t.Errorf("accepted %q", path)
		}
	}
	r := HTTPRule{Matches: []Match{{Path: "/api", Exact: true}}, Rewrite: &PrefixRewrite{ReplacePrefix: "/"}, Backends: backend}
	if ValidateHTTPRule(r, true) == nil {
		t.Fatal("exact rewrite accepted")
	}
	r.Matches[0].Exact = false
	r.Redirect = &Redirect{Scheme: "https", Status: 301}
	r.Backends = nil
	if ValidateHTTPRule(r, true) == nil {
		t.Fatal("redirect and rewrite accepted")
	}
}
func TestSharedRuleOrdering(t *testing.T) {
	rules := OrderedRules([]HTTPRule{{Matches: []Match{{Path: "/"}, {Path: "/api"}}}, {Matches: []Match{{Path: "/api/v1"}}}, {Matches: []Match{{Path: "/api", Exact: true}}}})
	for i, want := range []string{"/api", "/api/v1", "/api", "/"} {
		if rules[i].Matches[0].Path != want {
			t.Fatal(rules)
		}
	}
}
