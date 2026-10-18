package proxy

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/http/httpguts"
)

func ValidateHTTPRule(r HTTPRule, assigned bool) error {
	if len(r.Matches) > 64 || len(r.Backends) > 32 {
		return fmt.Errorf("route exceeds match/backend limit")
	}
	for _, m := range r.Matches {
		if m.Path != "" && ValidatePath(m.Path) != nil {
			return fmt.Errorf("unsupported HTTP path")
		}
		if m.Method != "" && !httpguts.ValidHeaderFieldName(m.Method) {
			return fmt.Errorf("invalid HTTP method")
		}
		for k, v := range m.Headers {
			if !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(v) {
				return fmt.Errorf("invalid header match")
			}
		}
	}
	if r.Rewrite != nil {
		if r.Redirect != nil || len(r.Matches) != 1 || r.Matches[0].Exact || r.Matches[0].Path == "" {
			return fmt.Errorf("prefix rewrite requires one PathPrefix match and no redirect")
		}
		if err := ValidatePath(r.Rewrite.ReplacePrefix); err != nil {
			return err
		}
	}
	for _, f := range []HeaderFilter{r.RequestHeaders, r.ResponseHeaders} {
		names := append([]string{}, f.Remove...)
		for k, v := range f.Set {
			if !httpguts.ValidHeaderFieldValue(v) {
				return fmt.Errorf("invalid header value")
			}
			names = append(names, k)
		}
		for k, v := range f.Add {
			if !httpguts.ValidHeaderFieldValue(v) {
				return fmt.Errorf("invalid header value")
			}
			names = append(names, k)
		}
		for _, name := range names {
			n := strings.ToLower(name)
			if !httpguts.ValidHeaderFieldName(name) || strings.HasPrefix(n, "x-forwarded-") || n == "forwarded" || n == "x-real-ip" || n == "host" || n == "connection" || n == "transfer-encoding" {
				return fmt.Errorf("header %s is reserved", name)
			}
		}
	}
	for _, d := range []string{r.RequestTimeout, r.BackendTimeout, r.BackendRequestTimeout} {
		if d != "" {
			v, e := time.ParseDuration(d)
			if e != nil || v < 0 || v > 24*time.Hour {
				return fmt.Errorf("invalid HTTP timeout")
			}
		}
	}
	if r.MaxRequestBodyBytes < 0 {
		return fmt.Errorf("invalid body limit")
	}
	total := 0
	for _, b := range r.Backends {
		total += b.Weight
		if b.Key == "" || b.Port == 0 || b.Weight < 0 || b.Weight > 1000 {
			return fmt.Errorf("invalid backend")
		}
		if assigned {
			if b.Service == "" || strings.ContainsAny(b.Service, " \r\n\t{}\"/") {
				return fmt.Errorf("invalid Service target")
			}
		} else {
			host, port, e := net.SplitHostPort(b.Address)
			ip, ie := netip.ParseAddr(host)
			if e != nil || ie != nil || !ip.IsPrivate() || ip.IsLoopback() || port != fmt.Sprint(b.Port) {
				return fmt.Errorf("backend must be a private cluster address and port")
			}
		}
	}
	if r.Redirect == nil && total == 0 {
		return fmt.Errorf("at least one backend must have positive weight")
	}
	if d := r.Redirect; d != nil {
		if len(r.Backends) > 0 || d.Status != 301 && d.Status != 302 || d.Scheme != "" && d.Scheme != "http" && d.Scheme != "https" {
			return fmt.Errorf("invalid redirect")
		}
		if d.Host != "" {
			u, e := url.Parse("https://" + d.Host)
			if e != nil || u.Hostname() != d.Host || strings.ContainsAny(d.Host, " \r\n\t{}") {
				return fmt.Errorf("invalid redirect hostname")
			}
		}
	}
	return nil
}

// ValidatePath accepts literal absolute paths only; configuration is never Caddy code.
func ValidatePath(path string) error {
	if len(path) > 2048 || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#{}*$%\\") || strings.ContainsFunc(path, unicode.IsControl) {
		return fmt.Errorf("path must be an absolute literal without query, fragment, placeholders or captures")
	}
	if _, err := url.PathUnescape(path); err != nil {
		return fmt.Errorf("invalid path escaping: %w", err)
	}
	return nil
}

// OrderedRules orders alternatives independently and preserves stable ties (Gateway API precedence).
func OrderedRules(rules []HTTPRule) []HTTPRule {
	var out []HTTPRule
	for _, r := range rules {
		matches := r.Matches
		if len(matches) == 0 {
			matches = []Match{{Path: "/"}}
		}
		for _, m := range matches {
			one := r
			one.Matches = []Match{m}
			out = append(out, one)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return MatchPrecedes(out[i].Matches[0], out[j].Matches[0]) })
	return out
}
func MatchPrecedes(a, b Match) bool {
	if a.Exact != b.Exact {
		return a.Exact
	}
	if x, y := len(strings.TrimRight(a.Path, "/")), len(strings.TrimRight(b.Path, "/")); x != y {
		return x > y
	}
	if (a.Method != "") != (b.Method != "") {
		return a.Method != ""
	}
	if len(a.Headers) != len(b.Headers) {
		return len(a.Headers) > len(b.Headers)
	}
	return len(a.Query) > len(b.Query)
}
