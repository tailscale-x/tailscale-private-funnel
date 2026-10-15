// Package policy contains the control-plane rules for gateway exposure.
package policy

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Rule grants a set of Tailscale principals permission to publish hostnames
// matching any HostnameRegexes pattern through one or more loadbalancers. Each pattern is
// matched against the complete, lower-case hostname.
type Rule struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	HostnameRegexes []string `json:"hostname_regexes"`
	Roles           []string `json:"roles"`
	GatewayIDs      []string `json:"gateway_ids"`
	Enabled         bool     `json:"enabled"`
}

// Document is persisted by the control service. An empty document denies all
// gateway exposure, which makes adding a gateway safe until an administrator
// explicitly creates a matching rule.
type Document struct {
	Revision uint64 `json:"revision"`
	Rules    []Rule `json:"rules"`
}

func (d Document) Validate() error {
	if len(d.Rules) > 200 {
		return fmt.Errorf("at most 200 exposure policy rules are supported")
	}
	ids := map[string]bool{}
	for i, rule := range d.Rules {
		if rule.ID != "" {
			if ids[rule.ID] {
				return fmt.Errorf("duplicate rule ID %s", rule.ID)
			}
			ids[rule.ID] = true
		}
		if len(rule.HostnameRegexes) == 0 || len(rule.HostnameRegexes) > 50 {
			return fmt.Errorf("policy rule %d requires 1 to 50 hostname patterns", i+1)
		}
		for j, pattern := range rule.HostnameRegexes {
			if strings.TrimSpace(pattern) == "" {
				return fmt.Errorf("policy rule %d pattern %d is empty", i+1, j+1)
			}
			if len(pattern) > 2048 {
				return fmt.Errorf("policy rule %d pattern %d is too long", i+1, j+1)
			}
			if _, err := regexp.Compile("^(?:" + pattern + ")$"); err != nil {
				return fmt.Errorf("policy rule %d pattern %d is invalid: %w", i+1, j+1, err)
			}
		}
	}
	return nil
}

// Allows returns whether one enabled rule authorizes this gateway and hostname.
// Any matching role or gateway selector is sufficient. Empty principal selectors
// allow any enrolled gateway. Load-balancer assignment is control-managed.
func (d Document) Allows(hostname string, gatewayIDs, roles []string) (bool, string) {
	if rule, ok := d.Match(hostname, gatewayIDs, roles); ok {
		name := rule.Name
		if name == "" {
			name = strings.Join(rule.HostnameRegexes, " OR ")
		}
		return true, name
	}
	return false, ""
}

// Match returns the first matching rule; rule order makes the provider choice
// deterministic. Gateway IDs are stable identifiers and remain case sensitive.
func (d Document) Match(hostname string, gatewayIDs, roles []string) (Rule, bool) {
	hostname = normalize(hostname)
	if ValidateHostname(hostname) != nil {
		return Rule{}, false
	}
	gatewaySet := map[string]bool{}
	for _, id := range gatewayIDs {
		gatewaySet[strings.TrimSpace(id)] = true
	}
	roleSet := set(roles)
	for _, rule := range d.Rules {
		if !rule.Enabled {
			continue
		}
		matched := false
		invalid := false
		for _, pattern := range rule.HostnameRegexes {
			if strings.TrimSpace(pattern) == "" || len(pattern) > 2048 {
				invalid = true
				break
			}
			ok, err := regexp.MatchString("^(?:"+pattern+")$", hostname)
			if err != nil {
				invalid = true
				break
			}
			matched = matched || ok
		}
		if invalid || !matched {
			continue
		}
		roleOK := false
		for _, role := range nonEmpty(rule.Roles) {
			if role == "*" || roleSet[normalize(role)] {
				roleOK = true
				break
			}
		}
		gatewayOK := false
		for _, id := range nonEmpty(rule.GatewayIDs) {
			if id == "*" || gatewaySet[id] {
				gatewayOK = true
				break
			}
		}
		if roleOK || gatewayOK || (len(nonEmpty(rule.Roles)) == 0 && len(nonEmpty(rule.GatewayIDs)) == 0) {
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

func containsID(values []string, value string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == value {
			return true
		}
	}
	return false
}
