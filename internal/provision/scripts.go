package provision

import (
	_ "embed"
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
