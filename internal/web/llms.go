package web

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"strings"
)

// agentDocumentation augments the generated contract with live installation
// context. It does not issue credentials, perform discovery, or trust Host headers.
func (c *Control) agentDocumentation(contract []byte) ([]byte, error) {
	var settings map[string]any
	if err := c.Store.GetJSON("tailnet", &settings); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	controlURL := stringValue(settings, "management_url", "")
	if controlURL == "" || validManagementURL(controlURL) != nil {
		controlURL = "http://CONTROL_TAILNET_HOST:8080"
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	const repository = "https://github.com/tailscale-x/tailscale-private-funnel"
	releaseURL := repository + "/releases/latest"
	downloadURL := releaseURL + "/download"
	installRef, installVersion := "main", ""
	versionNote := "This control is a development or unversioned build. These downloads use the latest stable release; a matching development binary must come from its verified development artifact."
	if releasePattern.MatchString(c.BuildVersion) {
		releaseURL = repository + "/releases/tag/" + c.BuildVersion
		downloadURL = repository + "/releases/download/" + c.BuildVersion
		installRef, installVersion = c.BuildVersion, " --version "+quote(c.BuildVersion)
		versionNote = "Download the matching control release: " + c.BuildVersion + "."
	}
	bootstrap := fmt.Sprintf(`## Download and install the CLI

%s

- [Release and assets](%s)
- [Linux amd64 binary](%s/tailscale-private-funnel-linux-amd64)
- [Linux arm64 binary](%s/tailscale-private-funnel-linux-arm64)
- [macOS arm64 binary](%s/tailscale-private-funnel-darwin-arm64)
- [SHA256SUMS](%s/SHA256SUMS): verify the downloaded binary before execution, then make it executable.

The installer selects a supported OS/architecture, verifies the release checksum,
and installs the binary in $XDG_BIN_HOME or $HOME/.local/bin:

`+"```sh"+`
curl -fsSL https://raw.githubusercontent.com/tailscale-x/tailscale-private-funnel/%s/scripts/install.sh | sh -s --%s
export PATH="${XDG_BIN_HOME:-$HOME/.local/bin}:$PATH"
tailscale-private-funnel version
`+"```"+`

## Use this control

Run management and non-control installer generation from an authorized machine
that can reach this private control over Tailscale. Downloading the CLI does not
join Tailscale or grant a control role. Targets need no existing Tailscale client.

`+"```sh"+`
tailscale-private-funnel manage whoami --control-url %s --output json
tailscale-private-funnel manage health --control-url %s --output json
tailscale-private-funnel manage --help
tailscale-private-funnel manage providers list --control-url %s --output json
tailscale-private-funnel native gateways create site-tailnet --type tailnet --control-url %s --output script | ssh ubuntu@TARGET 'sh -s'
tailscale-private-funnel k8s gateways create site-k8s --context TARGET_CONTEXT --namespace tailscale-private-funnel-main-system --control-url %s --output script
`+"```"+`

Replace TARGET and TARGET_CONTEXT before execution. Public create commands only
generate installers. Kubernetes generation does not load kubeconfig or contact a
cluster; execute its script on a machine with access to the explicit context and
an installed Funnel operator. Installer credentials are one-use, expire after
15 minutes, and must not be logged or committed. Use --dry-run for a credential-free,
non-executable preview. Inspect each command's --help for required settings.

`, versionNote, releaseURL, downloadURL, downloadURL, downloadURL, downloadURL,
		installRef, installVersion, quote(controlURL), quote(controlURL), quote(controlURL), quote(controlURL), quote(controlURL))
	foreground := fmt.Sprintf(`## Foreground local HTTP tunnel

With a compatible CLI, run on Linux or Apple Silicon macOS:

`+"```sh"+`
tailscale-private-funnel tunnel 8080 --hostname app.example.com --control-url %s
`+"```"+`

Unlike generated installers, tunnel phones home through an already-connected host
Tailscale daemon. Any verified peer that can reach control may request bootstrap;
no host tag or Funnel management role is required. Control issues a single-use,
ephemeral, 15-minute key restricted to %s. The process starts a separate embedded
tsnet identity with in-memory state and discards the key after joining. It never
retags, joins, logs out or changes the borrowed host daemon. Ctrl+C withdraws the
tunnel and logs out its embedded identity. Every restart requests a fresh key.

Use --tailscale host only for an already role-tagged host with LocalAPI Service
forwarding permission; that mode requests no key. No Docker, systemd or launchd.
Use --output json for newline-delimited progress events. HTTP backends must listen
on loopback; --local-address ::1 selects IPv6. Hostname policy, managed zones and
exact DNS ownership apply. Bootstrap grants runtime authority, never human admin.


`, quote(controlURL), identity.Tag(c.installation(), identity.TailnetGateway))
	return []byte(strings.Replace(string(contract), "## Start here", bootstrap+foreground+"## Start here", 1)), nil
}
