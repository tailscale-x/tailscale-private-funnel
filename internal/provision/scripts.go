package provision

import (
	_ "embed"
	"encoding/base64"
	"strings"
)

//go:embed assets/install-oci.py
var developmentInstaller string

func installerScript(version, image string) string {
	if strings.HasPrefix(version, "dev-") {
		return "command -v python3 >/dev/null || { echo 'Development bootstrap requires Python 3 for OCI extraction' >&2; exit 1; }\npython3 - " + Quote(image) + " <<'FUNNEL_INSTALL'\n" + developmentInstaller + "\nFUNNEL_INSTALL"
	}
	return "curl -fsSL " + Quote("https://raw.githubusercontent.com/tailscale-x/tailscale-private-funnel/"+version+"/scripts/install.sh") + " | sh -s -- --version " + Quote(version)
}

// TransferInstallerScript carries a stopped control's restore package on stdin.
// The payload is base64 encoded to prevent shell expansion or heredoc injection.
func TransferInstallerScript(version, image, target string, payload []byte) string {
	scope := "native"
	if target == "kubernetes" {
		scope = "k8s"
	}
	return "#!/bin/sh\nset -eu\numask 077\n" + installerScript(version, image) + "\nprintf '%s' " + Quote(base64.StdEncoding.EncodeToString(payload)) + " | base64 -d | \"${XDG_BIN_HOME:-$HOME/.local/bin}/tailscale-private-funnel\" " + scope + " runtime restore\n"
}
