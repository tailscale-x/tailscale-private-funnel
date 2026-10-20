// Package identity defines installation-scoped Tailscale authorization.
package identity

import (
	"fmt"
	"regexp"
	"slices"
)

type Role string

const (
	Control           Role = "control"
	LoadBalancer      Role = "loadbalancer"
	DockerGateway     Role = "docker-gateway"
	TailnetGateway    Role = "tailnet-gateway"
	KubernetesGateway Role = "k8s-gateway"
)

var Roles = []Role{Control, LoadBalancer, DockerGateway, TailnetGateway, KubernetesGateway}
var slug = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,22}[a-z0-9])?$`)

func ValidateInstallation(s string) error {
	if !slug.MatchString(s) {
		return fmt.Errorf("installation must be a lowercase DNS label of 1–24 characters")
	}
	return nil
}
func Tag(installation string, role Role) string {
	return "tag:tailscale-private-funnel-" + installation + "-" + string(role)
}

// IssuerTag is a credential identity, never a workload role or network grant.
func IssuerTag(installation string) string {
	return "tag:tailscale-private-funnel-" + installation + "-issuer"
}
func ServiceTag(installation string, control bool) string {
	kind := "application-service"
	if control {
		kind = "control-service"
	}
	return "tag:tailscale-private-funnel-" + installation + "-" + kind
}
func ControlService(installation string) string {
	return "svc:tailscale-private-funnel-" + installation + "-control"
}
func Authorize(installation string, role Role, tags []string) error {
	if err := ValidateInstallation(installation); err != nil {
		return err
	}
	if !slices.Contains(Roles, role) {
		return fmt.Errorf("unknown runtime role %q", role)
	}
	if !slices.Contains(tags, Tag(installation, role)) {
		return fmt.Errorf("required Tailscale role: %s", Tag(installation, role))
	}
	return nil
}

type Grant struct {
	Src []string `json:"src"`
	Dst []string `json:"dst"`
	IP  []string `json:"ip"`
}
type Policy struct {
	TagOwners     map[string][]string `json:"tagOwners"`
	Grants        []Grant             `json:"grants"`
	AutoApprovers struct {
		Services map[string][]string `json:"services"`
	} `json:"autoApprovers"`
}

func PolicyTemplate(installation string, port uint16) (Policy, error) {
	var p Policy
	if err := ValidateInstallation(installation); err != nil {
		return p, err
	}
	if port == 0 {
		return p, fmt.Errorf("control port is required")
	}
	issuer := IssuerTag(installation)
	p.TagOwners = map[string][]string{issuer: {"autogroup:admin"}}
	var runtime []string
	for _, role := range Roles {
		t := Tag(installation, role)
		runtime = append(runtime, t)
		p.TagOwners[t] = []string{"autogroup:admin", issuer}
	}
	control, app := ServiceTag(installation, true), ServiceTag(installation, false)
	p.TagOwners[control] = []string{"autogroup:admin", Tag(installation, Control)}
	p.TagOwners[app] = []string{"autogroup:admin", Tag(installation, Control)}
	p.Grants = []Grant{{runtime, []string{control}, []string{fmt.Sprintf("tcp:%d", port)}}, {[]string{"autogroup:owner", "autogroup:admin"}, []string{control}, []string{fmt.Sprintf("tcp:%d", port)}}, {[]string{Tag(installation, LoadBalancer)}, []string{app}, []string{"tcp:*"}}}
	// Service-only access may leave a newly approved host absent from an
	// existing client's peer map. ICMP keeps only these installation peers
	// discoverable; backend TCP/UDP remains accessible solely via Services.
	p.Grants = append(p.Grants, Grant{
		Src: []string{Tag(installation, LoadBalancer)},
		Dst: []string{Tag(installation, DockerGateway), Tag(installation, TailnetGateway), Tag(installation, KubernetesGateway)},
		IP:  []string{"icmp:*"},
	})
	// Runtime consumers also need visibility of replacement control-Service hosts.
	// This is peer discovery only; management remains on the control Service.
	p.Grants = append(p.Grants, Grant{
		Src: []string{Tag(installation, LoadBalancer), Tag(installation, DockerGateway), Tag(installation, TailnetGateway), Tag(installation, KubernetesGateway)},
		Dst: []string{Tag(installation, Control)},
		IP:  []string{"icmp:*"},
	})
	p.AutoApprovers.Services = map[string][]string{control: {Tag(installation, Control)}, app: {Tag(installation, DockerGateway), Tag(installation, TailnetGateway), Tag(installation, KubernetesGateway)}}
	return p, nil
}

func ValidRole(r Role) bool { return slices.Contains(Roles, r) }
