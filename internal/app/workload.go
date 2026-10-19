package app

import (
	"context"
	"errors"
	"os"

	"github.com/tailscale-x/tailscale-private-funnel/internal/workload"
)

func prepareWorkload(ctx context.Context, mode string, o *Options) (string, error) {
	if o.BootstrapURL == "" {
		o.BootstrapURL = os.Getenv("FUNNEL_BOOTSTRAP_URL")
	}
	if o.BootstrapTokenFile == "" {
		o.BootstrapTokenFile = "/var/run/secrets/funnel-operator/token"
	}
	if o.BootstrapCAFile == "" {
		o.BootstrapCAFile = "/etc/funnel-operator-ca/ca.crt"
	}
	if o.KubeNamespace == "" {
		o.KubeNamespace = os.Getenv("POD_NAMESPACE")
	}
	if o.ManagementURL == "" {
		o.ManagementURL = os.Getenv("FUNNEL_CONTROL_URL")
	}
	if o.BootstrapURL == "" {
		return "", errors.New("operator bootstrap URL required for disposable workload")
	}
	b, e := workload.Issue(ctx, o.BootstrapURL, o.BootstrapTokenFile, o.BootstrapCAFile)
	if e != nil {
		return "", e
	}
	if b.Mode != mode || o.Installation != "" && o.Installation != b.Installation {
		return "", errors.New("operator returned a mismatched installation or role")
	}
	if o.ComponentID != "" && o.ComponentID != b.ComponentId {
		return "", errors.New("operator component binding mismatch")
	}
	o.ComponentID = b.ComponentId
	o.InstanceID = b.InstanceId
	o.ParentID = b.ParentId
	o.NodeUID = b.NodeUid
	o.NodeName = b.NodeName
	o.Installation = b.Installation
	o.Ephemeral = true
	return b.AuthKey, nil
}
