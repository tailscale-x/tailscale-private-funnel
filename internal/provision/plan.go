// Package provision is the single deployment planner and local execution engine.
package provision

import (
	"fmt"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
	"net/mail"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"google.golang.org/protobuf/proto"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
var versionPattern = regexp.MustCompile(`^(v[0-9]+\.[0-9]+\.[0-9]+|dev-[a-zA-Z0-9._-]+)$`)

const ImageRepository = "ghcr.io/tailscale-x/tailscale-private-funnel"
const ChartRepository = "oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel"

// Resolve is pure: it performs no network calls, enrollment, filesystem writes or key creation.
func Resolve(in *v1.DeploymentRequest, managementURL, version string) (*v1.DeploymentInstructions, error) {
	if in == nil {
		return nil, fmt.Errorf("deployment is required")
	}
	d := proto.Clone(in).(*v1.DeploymentRequest)
	switch d.Mode {
	case "operator", "control", "loadbalancer", "docker-gateway", "tailnet-gateway", "k8s-gateway":
	default:
		return nil, fmt.Errorf("unsupported mode")
	}
	if d.Target != "native" && d.Target != "kubernetes" {
		return nil, fmt.Errorf("explicit target native or kubernetes is required")
	}
	if (d.Mode == "k8s-gateway" || d.Mode == "operator") && d.Target != "kubernetes" {
		return nil, fmt.Errorf("k8s-gateway requires Kubernetes")
	}
	if d.Target == "kubernetes" && d.Mode != "operator" && d.Mode != "control" && d.Mode != "k8s-gateway" && d.Mode != "loadbalancer" {
		return nil, fmt.Errorf("this gateway mode is native-only")
	}
	if d.Runtime == "" {
		d.Runtime = "systemd"
		if d.Target == "kubernetes" {
			d.Runtime = "operator"
		}
	}
	if d.Target == "native" && d.Runtime != "systemd" {
		return nil, fmt.Errorf("native runtime must be systemd")
	}
	if d.Target == "kubernetes" && d.Runtime != "operator" {
		return nil, fmt.Errorf("Kubernetes runtime must be operator")
	}
	if d.ResourceName == "" {
		d.ResourceName = d.Hostname
	}
	if d.ResourceName == "" {
		d.ResourceName = d.Mode
	}
	if d.Hostname == "" {
		d.Hostname = d.ResourceName
	}
	if len(d.Hostname) > 63 || !namePattern.MatchString(d.Hostname) {
		return nil, fmt.Errorf("hostname must be a DNS label of at most 63 characters")
	}
	d.DisplayName = strings.TrimSpace(d.DisplayName)
	if len([]rune(d.DisplayName)) > 128 {
		return nil, fmt.Errorf("display name exceeds 128 characters")
	}
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		d.Name = d.Hostname
	}
	if len([]rune(d.Name)) > 128 {
		return nil, fmt.Errorf("name exceeds 128 characters")
	}
	if !versionPattern.MatchString(version) {
		return nil, fmt.Errorf("a pinned release or development version is required")
	}
	if d.StorageClass != "" || d.StorageSize != "" {
		if d.Target != "kubernetes" || (d.Mode != "control" && d.Mode != "loadbalancer") {
			return nil, fmt.Errorf("PVC storage options apply only to Kubernetes controls and load balancers")
		}
		if d.StorageClass != "" {
			if err := validation.IsDNS1123Subdomain(d.StorageClass); len(err) > 0 {
				return nil, fmt.Errorf("invalid StorageClass name")
			}
		}
		if d.StorageSize != "" {
			q, err := resource.ParseQuantity(d.StorageSize)
			if err != nil || q.Sign() <= 0 {
				return nil, fmt.Errorf("storage size must be a positive Kubernetes quantity")
			}
		}
	}
	u, e := url.Parse(managementURL)
	if d.Mode != "control" && d.Mode != "operator" && (e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("a tailnet control HTTP(S) origin is required")
	}
	if d.Mode != "control" && len(d.Administrators) > 0 {
		return nil, fmt.Errorf("administrator selectors apply only to control bootstrap")
	}
	if d.Mode == "loadbalancer" {
		if err := policy.ValidateHostname(d.PublicHostname); err != nil {
			return nil, fmt.Errorf("a valid public load-balancer hostname is required")
		}
		address, err := mail.ParseAddress(d.CertificateEmail)
		if err != nil || address.Address != d.CertificateEmail {
			return nil, fmt.Errorf("a certificate contact email is required")
		}
	} else if d.PublicHostname != "" || d.CertificateEmail != "" {
		return nil, fmt.Errorf("public hostname and certificate email apply only to load balancers")
	}
	if d.StateDir == "" {
		d.StateDir = "/var/lib/tailscale-private-funnel/native/" + ResourceKind(d.Mode) + "/" + d.ResourceName + "/state"
	}
	if d.DataDir == "" {
		d.DataDir = "/var/lib/tailscale-private-funnel/native/" + ResourceKind(d.Mode) + "/" + d.ResourceName + "/data"
	}
	for _, path := range []string{d.StateDir, d.DataDir} {
		if !filepath.IsAbs(path) || filepath.Clean(path) == "/" || strings.ContainsAny(path, "\x00\n\r") {
			return nil, fmt.Errorf("state/data paths must be explicit safe absolute directories")
		}
	}
	d.StateDir = filepath.Clean(d.StateDir)
	d.DataDir = filepath.Clean(d.DataDir)
	if d.StateDir == d.DataDir || strings.HasPrefix(d.StateDir, d.DataDir+"/") || strings.HasPrefix(d.DataDir, d.StateDir+"/") {
		return nil, fmt.Errorf("state/data directories must be distinct and nonoverlapping")
	}
	if d.Installation == "" {
		d.Installation = "main"
	}
	if len(d.Installation) > 31 || !namePattern.MatchString(d.Installation) {
		return nil, fmt.Errorf("invalid installation name")
	}
	if d.ResourceName == "" {
		d.ResourceName = d.Hostname
	}
	if len(d.ResourceName) > 53 || !namePattern.MatchString(d.ResourceName) {
		return nil, fmt.Errorf("invalid resource name")
	}
	if d.GatewayClass == "" {
		d.GatewayClass = "tailscale-private-funnel-" + d.ResourceName
	}
	if d.IngressClass == "" {
		d.IngressClass = d.GatewayClass
	}
	if d.Mode != "docker-gateway" && (len(d.LabelKeys) > 0 || len(d.DenyNetworks) > 0) {
		return nil, fmt.Errorf("Docker discovery settings apply only to Docker gateways")
	}
	for _, key := range d.LabelKeys {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`).MatchString(key) {
			return nil, fmt.Errorf("invalid Docker label alias")
		}
	}
	for _, pattern := range d.DenyNetworks {
		if _, err := filepath.Match(pattern, "network"); err != nil {
			return nil, fmt.Errorf("invalid Docker deny pattern")
		}
	}
	if d.Mode != "k8s-gateway" && len(d.WatchNamespaces) > 0 || len(d.NodeSelector) > 0 && (d.Target != "kubernetes" || d.Mode == "operator") {
		return nil, fmt.Errorf("Kubernetes discovery settings apply only to Kubernetes gateways")
	}
	for key, value := range d.NodeSelector {
		if len(kvalidation.IsQualifiedName(key)) > 0 || len(kvalidation.IsValidLabelValue(value)) > 0 {
			return nil, fmt.Errorf("invalid Kubernetes node selector")
		}
	}
	for _, class := range []string{d.GatewayClass, d.IngressClass} {
		if len(kvalidation.IsDNS1123Subdomain(class)) > 0 {
			return nil, fmt.Errorf("invalid Kubernetes class name")
		}
	}
	for _, ns := range append(append([]string{}, d.WatchNamespaces...), d.ManagedNamespaces...) {
		if len(ns) > 63 || !namePattern.MatchString(ns) {
			return nil, fmt.Errorf("invalid watched namespace")
		}
	}
	plan := &v1.DeploymentInstructions{Deployment: d, ManagementUrl: strings.TrimRight(managementURL, "/"), Version: version, Image: ImageRepository + ":" + version, Chart: ChartRepository, ChartVersion: strings.TrimPrefix(version, "v"), Namespace: "tailscale-private-funnel-" + d.Installation + "-system"}
	if strings.HasPrefix(version, "dev-") {
		plan.ChartVersion = "0.0.0-" + version
	}
	plan.Prerequisites = []string{"Administrator access to the selected Funnel control", "Existing shared tag grant; provisioning does not modify ACLs", "Persistent identity and application storage"}
	if d.Target == "native" {
		plan.Prerequisites = append(plan.Prerequisites, "Linux and systemd; root or noninteractive sudo")
		if d.Mode == "docker-gateway" {
			plan.Prerequisites = append(plan.Prerequisites, "Working Docker access")
		}
	} else {
		plan.Prerequisites = append(plan.Prerequisites, "Explicit --context; Kubernetes operator installed for the target namespace", "Existing watched namespaces and persistent storage")
	}

	if d.Mode == "operator" {
		if len(d.ManagedNamespaces) == 0 {
			return nil, fmt.Errorf("operator requires at least one managed namespace")
		}
		plan.Namespace = "tailscale-private-funnel-k8s-operator-system"
		plan.Prerequisites = []string{"Explicit Kubernetes context with permission to install CRDs and scoped operator RBAC", "Operator is stateless; components own their persistent identities and storage"}
	}
	if d.Mode == "control" {
		plan.Prerequisites = []string{"Tailscale OAuth authority and protected credential files on the executing target", "Persistent identity and database storage; management remains tailnet-only"}
	}

	switch d.Mode {
	case "control":
		plan.Prerequisites = append(plan.Prerequisites, "Protected target OAuth files: auth_keys, devices:core and services scoped to tag:tailscale-private-funnel; users:read for verified Owner/Admin roles", "An independent control has its own database and private setup URL")
	case "docker-gateway":
		plan.Prerequisites = append(plan.Prerequisites, "The supervisor requires host Docker permissions; relay containers have no Docker socket or Tailscale credentials", "Services require a user-defined target network; local deny patterns exclude sensitive networks; label removal withdraws the exposure")
		plan.Example = "# Add to an application container's Compose labels:\nlabels:\n  tailscale-private-funnel.expose: \"8080:app.example.com:http\"\n# The hostname must match an enabled control exposure policy."
	case "tailnet-gateway":
		plan.Prerequisites = append(plan.Prerequisites, "No Docker installation is required", "Assign additional tags and grants externally in Tailscale; peer visibility is not proof of connectivity", "After enrollment use Exposures → Expose a tailnet machine and select this gateway")
	case "k8s-gateway":
		plan.Prerequisites = append(plan.Prerequisites, "Install pinned Gateway API v1.5.1 standard CRDs before creating the gateway", "The operator manages workload and Secret permissions only in installation namespaces; routing metadata access is cluster-scoped and watch namespaces filter processing", "Forwarders use ready endpoints on their own Kubernetes node; missing local endpoints withdraw that Service host")
		plan.Example = fmt.Sprintf("# Example routing resources; replace app.example.com and checkout with your workload.\napiVersion: gateway.networking.k8s.io/v1\nkind: Gateway\nmetadata: {name: public-http}\nspec:\n  gatewayClassName: %s\n  listeners:\n    - {name: http, protocol: HTTP, port: 80}\n---\napiVersion: gateway.networking.k8s.io/v1\nkind: HTTPRoute\nmetadata: {name: checkout}\nspec:\n  parentRefs: [{name: public-http}]\n  hostnames: [app.example.com]\n  rules:\n    - backendRefs: [{name: checkout, port: 8080}]\n---\napiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata: {name: another-example}\nspec:\n  ingressClassName: %s\n  rules:\n    - host: other.example.com\n      http:\n        paths:\n          - path: /\n            pathType: Prefix\n            backend:\n              service: {name: checkout, port: {number: 8080}}\n", d.GatewayClass, d.IngressClass)
	case "loadbalancer":
		plan.Prerequisites = append(plan.Prerequisites, "Public TCP 80/443, a managed DNS zone, and policy-authorized exposures are required for public HTTPS", "Updates preserve the identity and certificates; roll back by updating to the prior pinned version")
	}
	if d.Namespace != "" {
		if len(kvalidation.IsDNS1123Label(d.Namespace)) > 0 {
			return nil, fmt.Errorf("invalid namespace")
		}
		plan.Namespace = d.Namespace
	}
	for _, step := range []struct{ id, title, description string }{{"preflight", "Check prerequisites", "Validate target, permissions, artifacts and existing identity before enrollment"}, {"enroll", "Create enrollment", "Control mints a one-use bootstrap key; secrets remain temporary"}, {"install", "Install and start", "Install verified artifacts and persistent service resources"}, {"verify", "Verify readiness", "Wait for enrolled identity and fresh signed application health"}} {
		plan.Steps = append(plan.Steps, &v1.DeploymentStep{Id: step.id, Title: step.title, Description: step.description})
	}
	plan.Commands = Commands(plan)
	plan.Script = plan.Commands["create"]
	return plan, nil
}
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func ValidVersion(version string) bool { return versionPattern.MatchString(version) }
