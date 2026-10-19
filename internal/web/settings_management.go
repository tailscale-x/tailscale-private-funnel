package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var releasePattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

type nodeDeployment struct {
	Version          string `json:"version,omitempty"`
	ContextHint      string `json:"context_hint,omitempty"`
	DeploymentTarget string `json:"deployment_target,omitempty"`
	AddressSource    string `json:"address_source,omitempty"`
	ResourceName     string `json:"resource_name,omitempty"`
	PublicService    string `json:"public_service,omitempty"`
	InstallationName string `json:"installation_name,omitempty"`
	WatchNamespaces  string `json:"watch_namespaces,omitempty"`
	NodeSelector     string `json:"node_selector,omitempty"`

	KubernetesNamespace  string `json:"kubernetes_namespace,omitempty"`
	GatewayClass         string `json:"gateway_class,omitempty"`
	IngressClass         string `json:"ingress_class,omitempty"`
	StateDir             string `json:"state_dir,omitempty"`
	DataDir              string `json:"data_dir,omitempty"`
	LoadBalancerHostname string `json:"loadbalancer_hostname"`
	CertEmail            string `json:"cert_email"`
	LabelKeys            string `json:"label_keys"`
	DenyNetworks         string `json:"deny_networks"`
}

func (c *Control) nodeDeployment(id string) (nodeDeployment, error) {
	var d nodeDeployment
	err := c.Store.GetJSON("node_deployment:"+id, &d)
	if errors.Is(err, sql.ErrNoRows) {
		return d, nil
	}
	return d, err
}

func validManagementURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Use an HTTP or HTTPS management URL without credentials, query or fragment")
	}
	return nil
}
func (c *Control) checkRelease(ctx context.Context, version string) error {
	// The running development artifact has already passed deployment verification.
	// Other development tags are not assumed to exist or to be compatible.
	if strings.HasPrefix(version, "dev-") && version == c.BuildVersion {
		return nil
	}
	if !releasePattern.MatchString(version) {
		return fmt.Errorf("Use a published version such as v1.1.0")
	}
	if c.ReleaseCheck != nil {
		return c.ReleaseCheck(ctx, version)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for _, asset := range []string{"tailscale-private-funnel-linux-amd64", "tailscale-private-funnel-linux-arm64", "SHA256SUMS"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, "https://github.com/tailscale-x/tailscale-private-funnel/releases/download/"+version+"/"+asset, nil)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("Unable to verify release; retry before saving")
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("Release %s is missing %s", version, asset)
		}
	}
	return nil
}
