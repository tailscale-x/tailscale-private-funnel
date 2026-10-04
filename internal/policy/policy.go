// Package policy contains the control-plane rules for ingress exposure.
package policy

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Rule grants a set of Tailscale principals permission to publish hostnames
// matching HostnameRegex through one or more loadbalancers. HostnameRegex is
// matched against the complete, lower-case hostname.
type Rule struct {
	Name                  string   `json:"name"`
	HostnameRegex         string   `json:"hostname_regex"`
	Roles                 []string `json:"roles"`
	IngressIDs            []string `json:"ingress_ids"`
	LoadBalancerHostnames []string `json:"loadbalancer_hostnames"`
	Enabled               bool     `json:"enabled"`
}

// Document is persisted by the control service. An empty document denies all
// ingress exposure, which makes adding a ingress safe until an administrator
// explicitly creates a matching rule.
type Document struct {
	Rules []Rule `json:"rules"`
}

func (d Document) Validate() error {
	if len(d.Rules) > 200 {
		return fmt.Errorf("at most 200 exposure policy rules are supported")
	}
	for i, rule := range d.Rules {
		if len(rule.HostnameRegex) > 2048 {
			return fmt.Errorf("policy rule %d hostname regex is too long", i+1)
		}
		if strings.TrimSpace(rule.HostnameRegex) == "" {
			return fmt.Errorf("policy rule %d has no hostname_regex", i+1)
		}
		if _, err := regexp.Compile("^(?:" + rule.HostnameRegex + ")$"); err != nil {
			return fmt.Errorf("policy rule %d has invalid hostname_regex: %w", i+1, err)
		}
		if len(nonEmpty(rule.Roles)) == 0 && len(nonEmpty(rule.IngressIDs)) == 0 {
			return fmt.Errorf("policy rule %d must select at least one role or ingress", i+1)
		}
	}
	return nil
}

// Allows returns whether one enabled rule authorizes this ingress, hostname,
// and loadbalancer. Any matching role or ingress selector is sufficient.
func (d Document) Allows(hostname, loadbalancer string, ingressIDs, roles []string) (bool, string) {
	if rule, ok := d.Match(hostname, loadbalancer, ingressIDs, roles); ok {
		name := rule.Name
		if name == "" {
			name = rule.HostnameRegex
		}
		return true, name
	}
	return false, ""
}

// Match returns the first matching rule; rule order makes the provider choice
// deterministic. Ingress IDs are stable identifiers and remain case sensitive.
func (d Document) Match(hostname, loadbalancer string, ingressIDs, roles []string) (Rule, bool) {
	hostname = normalize(hostname)
	loadbalancer = normalize(loadbalancer)
	if ValidateHostname(hostname) != nil || ValidateHostname(loadbalancer) != nil {
		return Rule{}, false
	}
	ingressSet := map[string]bool{}
	for _, id := range ingressIDs {
		ingressSet[strings.TrimSpace(id)] = true
	}
	roleSet := set(roles)
	for _, rule := range d.Rules {
		if !rule.Enabled {
			continue
		}
		matched, err := regexp.MatchString("^(?:"+rule.HostnameRegex+")$", hostname)
		if err != nil || !matched {
			continue
		}
		if len(nonEmpty(rule.LoadBalancerHostnames)) > 0 && !containsNormalized(rule.LoadBalancerHostnames, loadbalancer) {
			continue
		}
		roleOK := false
		for _, role := range nonEmpty(rule.Roles) {
			if role == "*" || roleSet[normalize(role)] {
				roleOK = true
				break
			}
		}
		ingressOK := false
		for _, id := range nonEmpty(rule.IngressIDs) {
			if id == "*" || ingressSet[id] {
				ingressOK = true
				break
			}
		}
		if roleOK || ingressOK {
			return rule, true
		}
	}
	return Rule{}, false
}

// ValidateHostname deliberately accepts DNS hostnames rather than URLs, IPs,
// wildcard names or Caddy expressions. International names use their ASCII
// punycode form so one canonical string is used for policy and DNS.
func ValidateHostname(hostname string) error {
	hostname = normalize(hostname)
	if _, err := netip.ParseAddr(hostname); err == nil {
		return fmt.Errorf("an IP address is not a DNS hostname")
	}
	if len(hostname) > 253 || !strings.Contains(hostname, ".") {
		return fmt.Errorf("a complete DNS hostname is required")
	}
	for _, label := range strings.Split(hostname, ".") {
		if len(label) < 1 || len(label) > 63 || !hostnameLabel.MatchString(label) {
			return fmt.Errorf("invalid DNS hostname %q", hostname)
		}
	}
	return nil
}

var hostnameLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

func nonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func set(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value = normalize(value); value != "" {
			out[value] = true
		}
	}
	return out
}

func containsNormalized(values []string, want string) bool {
	for _, value := range values {
		if normalize(value) == want {
			return true
		}
	}
	return false
}
