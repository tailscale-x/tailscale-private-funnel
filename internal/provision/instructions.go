package provision

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"google.golang.org/protobuf/proto"
)

func ResourceKind(mode string) string {
	switch mode {
	case "operator":
		return "operator"
	case "control":
		return "controls"
	case "loadbalancer":
		return "loadbalancers"
	default:
		return "gateways"
	}
}
func Arguments(p *v1.DeploymentInstructions, action string) []string {
	d := p.Deployment
	scope := "native"
	if d.Target == "kubernetes" {
		scope = "k8s"
	}
	args := []string{"tailscale-private-funnel", scope, ResourceKind(d.Mode), action}
	if d.Mode != "operator" {
		args = append(args, d.ResourceName)
	}
	if d.Target == "kubernetes" {
		context := d.Context
		if context == "" {
			context = "REPLACE_WITH_CONTEXT"
		}
		args = append(args, "--context", context)
		if d.Mode != "operator" {
			args = append(args, "--namespace", p.Namespace)
		}
	}
	if action != "create" {
		if action == "delete" {
			args = append(args, "--yes")
		}
		if action == "update" {
			args = append(args, "--version", p.Version)
		}

		return args
	}
	if action == "create" && d.Database != nil {
		b := d.Database
		args = append(args, "--database", b.Type)
		for _, pair := range [][2]string{{"--database-url-file", b.UrlFile}, {"--database-ca-file", b.CaFile}, {"--database-tls-mode", b.TlsMode}, {"--database-secret", b.ConnectionSecret}, {"--database-secret-key", b.ConnectionSecretKey}, {"--database-ca-secret", b.CaSecret}, {"--database-ca-secret-key", b.CaSecretKey}} {
			if pair[1] != "" {
				args = append(args, pair[0], pair[1])
			}
		}
	}
	if d.Wait {
		args = append(args, "--wait")
	}
	if d.TimeoutSeconds > 0 {
		args = append(args, "--timeout", fmt.Sprintf("%ds", d.TimeoutSeconds))
	}
	if d.Mode == "operator" {
		args = append(args, "--version", p.Version)
		for _, ns := range d.ManagedNamespaces {
			args = append(args, "--managed-namespace", ns)
		}
		if len(d.ManagedNamespaces) == 0 {
			args = append(args, "--managed-namespace", p.Namespace)
		}
		return args
	}
	args = append(args, "--hostname", d.Hostname, "--version", p.Version, "--installation", d.Installation)
	if d.Target == "native" {
		args = append(args, "--tailscale", d.TailscaleConnection)
		if d.TailscaleSocket != "" {
			args = append(args, "--tailscale-socket", d.TailscaleSocket)
		}
	}
	if d.Target == "kubernetes" {
		args = append(args, "--key-issuer-secret", d.KeyIssuerSecret)
		if d.Mode == "control" {
			args = append(args, "--services-secret", d.ServicesSecret, "--users-secret", d.UsersSecret)
		}
	}
	if d.Target == "kubernetes" {
		if d.StorageClass != "" {
			args = append(args, "--storage-class", d.StorageClass)
		}
		if d.StorageSize != "" {
			args = append(args, "--storage-size", d.StorageSize)
		}
	}
	if d.DisplayName != "" {
		args = append(args, "--display-name", d.DisplayName)
	}
	if d.Target == "native" {
		args = append(args, "--state-dir", d.StateDir, "--data-dir", d.DataDir)
	}
	if d.Mode == "control" {
		args = append(args, "--tailnet", d.Tailnet)
		for _, selector := range d.Administrators {
			args = append(args, "--administrator", selector)
		}
		id, secret := d.OauthClientIdFile, d.OauthClientSecretFile
		if id == "" {
			id = "/etc/tailscale-private-funnel/bootstrap/ts-client-id"
		}
		if secret == "" {
			secret = "/etc/tailscale-private-funnel/bootstrap/ts-client-secret"
		}
		if d.Target == "native" {
			if d.TailscaleConnection != "host" {
				args = append(args, "--oauth-client-id-file", id, "--oauth-client-secret-file", secret)
			}
			args = append(args, "--services-client-id-file", d.ServicesClientIdFile, "--services-client-secret-file", d.ServicesClientSecretFile, "--users-client-id-file", d.UsersClientIdFile, "--users-client-secret-file", d.UsersClientSecretFile)
		}
	} else {
		args = append(args, "--control-url", p.ManagementUrl)
	}
	if d.Target == "native" && d.Mode != "control" && d.Mode != "loadbalancer" {
		args = append(args, "--type", strings.TrimSuffix(d.Mode, "-gateway"))
	}
	if d.Mode == "loadbalancer" {
		args = append(args, "--public-hostname", d.PublicHostname, "--certificate-email", d.CertificateEmail)
	}
	if d.Mode == "k8s-gateway" {
		args = append(args, "--gateway-class", d.GatewayClass, "--ingress-class", d.IngressClass)
		for _, ns := range d.WatchNamespaces {
			args = append(args, "--watch-namespace", ns)
		}
	}
	keys := []string{}
	for key := range d.NodeSelector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--node-selector", key+"="+d.NodeSelector[key])
	}
	for _, v := range d.LabelKeys {
		args = append(args, "--label-key", v)
	}
	for _, v := range d.DenyNetworks {
		args = append(args, "--deny-network", v)
	}
	return args
}
func QuoteArguments(args []string) string {
	out := make([]string, len(args))
	for i, v := range args {
		out[i] = Quote(v)
	}
	return strings.Join(out, " ")
}
func Commands(p *v1.DeploymentInstructions) map[string]string {
	out := map[string]string{}
	for _, action := range []string{"create", "get", "update", "delete"} {
		out[action] = QuoteArguments(Arguments(p, action))
	}
	out["installer"] = out["create"] + " --output script"
	if p.Deployment.Target == "native" {
		for _, action := range []string{"start", "stop", "restart", "logs"} {
			out[action] = QuoteArguments(Arguments(p, action))
		}
		out["ssh"] = out["create"] + " --output script | ssh ubuntu@TARGET_HOST 'sh -s'"
	}
	return out
}

// InstallerScript transports the canonical bundle only through stdin.
func InstallerScript(p *v1.DeploymentInstructions) string {
	scope := "native"
	if p.Deployment.Target == "kubernetes" {
		scope = "k8s"
	}
	copy := proto.Clone(p).(*v1.DeploymentInstructions)
	copy.Script = ""
	copy.Commands = nil
	b, _ := json.Marshal(copy)
	command := "\"${XDG_BIN_HOME:-$HOME/.local/bin}/tailscale-private-funnel\" " + scope + " runtime install"
	return "#!/bin/sh\nset -eu\numask 077\n" + installerScript(p.Version, p.Image) + "\n" + command + " <<'FUNNEL_BOOTSTRAP'\n" + string(b) + "\nFUNNEL_BOOTSTRAP\n"
}
func RemoteOutput(p *v1.DeploymentInstructions, format string, dry bool) (string, error) {
	if dry {
		p.DryRun = true
		if format == "script" {
			return "#!/bin/sh\n# Dry-run preview: no credentials were created.\nprintf '%s\\n' 'Dry-run output is not an executable installer' >&2\nexit 1\n", nil
		}
		p.Credentials = nil
		p.Script = ""
		if format == "instructions" {
			return "Dry-run preview only. No credentials were created.\n" + p.Commands["create"] + "\n", nil
		}
	}
	switch format {
	case "script":
		return InstallerScript(p), nil
	case "json":
		copy := proto.Clone(p).(*v1.DeploymentInstructions)
		if !dry {
			copy.Script = InstallerScript(p)
		}
		b, e := json.Marshal(copy)
		return string(b) + "\n", e
	case "instructions":
		guidance := "Embedded native bootstrap keys expire after 15 minutes; no host Tailscale is required. Native stop preserves identity and data."
		if p.Deployment.TailscaleConnection == "host" {
			guidance = "Host reuse requires an already-connected role-tagged daemon and LocalAPI permissions; no join secret is included. Stop preserves borrowed host configuration."
		} else if p.Deployment.Target == "kubernetes" {
			guidance = "The execution machine needs Kubernetes access. Workloads obtain fresh disposable identities through protected installation issuer Secrets; persistent application data is retained."
		}
		return fmt.Sprintf("Run this installer on the target host, or a machine with the selected Kubernetes context. Version: %s\n%s Delete removes only owned resources; removing a Tailscale role tag or device ends that role authority.\n\n%s", p.Version, guidance, InstallerScript(p)), nil
	default:
		return "", fmt.Errorf("--output must be instructions, script, or json")
	}
}
