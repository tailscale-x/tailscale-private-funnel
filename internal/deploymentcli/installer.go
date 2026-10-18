package deploymentcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/manage"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func creationOutput(c *cobra.Command, f *Flags) {
	f.Output = "instructions"
	c.Flags().Lookup("output").DefValue = "instructions"
	c.Flags().Lookup("output").Usage = "output: instructions, script, or json (installer generation)"
	c.Flags().Lookup("wait").Usage = "wait for readiness during target-side execution"
	c.Flags().Lookup("timeout").Usage = "target execution timeout; generation does not wait for deployment"
	c.Short = "Generate an installer; execute the output on the target"
}

func generateInstaller(c *cobra.Command, in *v1.DeploymentRequest, f Flags) error {
	url := f.ControlURL
	if in.Mode == "control" && !f.DryRun && in.Tailnet == "" {
		return errors.New("control installer generation requires --tailnet; OAuth-file paths refer to files on the execution machine")
	}
	if in.Mode != "control" && in.Mode != "operator" {
		var err error
		url, err = manage.ResolveURL(url)
		if err != nil {
			return err
		}
	}
	p, err := provision.Resolve(in, url, f.Version)
	if err != nil {
		return err
	}
	if in.Mode != "control" && in.Mode != "operator" && !f.DryRun {
		rpc, err := apiClient(url)
		if err != nil {
			return err
		}
		// --timeout describes target execution; issuing authorization has its own bound.
		ctx, cancel := context.WithTimeout(c.Context(), time.Minute)
		defer cancel()
		res, err := rpc.Authorize(ctx, connect.NewRequest(in))
		if err != nil {
			return err
		}
		p = res.Msg
	}
	delivered := false
	if p.Credentials != nil {
		defer func() {
			if delivered {
				return
			}
			// A failed output must not leave a redeemable enrollment behind.
			// Tailscale's independently issued key remains bounded by its TTL.
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			client := funnelv1connect.NewManagementServiceClient(&http.Client{Transport: transport}, url)
			if _, e := client.RevokeEnrollment(cleanup, connect.NewRequest(&v1.ResourceID{Id: p.Credentials.EnrollmentId})); e != nil {
				fmt.Fprintln(c.ErrOrStderr(), "Installer output failed; enrollment revocation could not be confirmed. Revoke the pending enrollment in control.")
			}
		}()
	}
	rendered, err := provision.RemoteOutput(p, f.Output, f.DryRun)
	if err != nil {
		return err
	}
	_, err = io.WriteString(c.OutOrStdout(), rendered)
	delivered = err == nil
	return err
}

// Installer is hidden: only generated scripts call it. There is no authorization
// request here; the supplied bundle is the sole source of deployment settings.
func Installer(target string) *cobra.Command {
	c := &cobra.Command{Use: "install", Hidden: true, Args: cobra.NoArgs}
	c.RunE = func(c *cobra.Command, _ []string) error {
		if target == "native" {
			// Forward stdin to sudo before decoding; never reopen or persist credentials.
			if handled, err := elevate(c, Flags{}); handled {
				return err
			}
		}
		p, err := readInstallerBundle(c.InOrStdin(), target)
		if err != nil {
			return err
		}
		var root *cobra.Command
		if target == "native" {
			root = nativeCommands(p.Version, p)
		} else {
			root = kubernetesCommands(p.Version, p)
		}
		root.SilenceErrors, root.SilenceUsage = true, true
		root.SetOut(c.OutOrStdout())
		root.SetErr(c.ErrOrStderr())
		root.SetIn(c.InOrStdin())
		root.SetArgs(provision.Arguments(p, "create")[2:])
		return root.ExecuteContext(c.Context())
	}
	return c
}

func readInstallerBundle(r io.Reader, target string) (*v1.DeploymentInstructions, error) {
	b, err := io.ReadAll(io.LimitReader(r, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 2<<20 {
		return nil, errors.New("bootstrap bundle exceeds size limit")
	}
	var p v1.DeploymentInstructions
	if err = protojson.Unmarshal(b, &p); err != nil {
		return nil, errors.New("invalid bootstrap bundle")
	}
	if p.DryRun {
		return nil, errors.New("dry-run previews cannot be installed; generate an installer without --dry-run")
	}
	if p.Deployment == nil || p.Deployment.Target != target {
		return nil, errors.New("bootstrap deployment target mismatch")
	}
	resolved, err := provision.Resolve(p.Deployment, p.ManagementUrl, p.Version)
	if err != nil {
		return nil, err
	}
	if p.Image != resolved.Image || p.Namespace != resolved.Namespace || p.Chart != resolved.Chart || p.ChartVersion != resolved.ChartVersion || !proto.Equal(p.Deployment, resolved.Deployment) {
		return nil, errors.New("bootstrap deployment settings mismatch")
	}
	if target == "kubernetes" && p.Deployment.Context == "" {
		return nil, errors.New("bootstrap requires an explicit Kubernetes context")
	}
	if p.Deployment.Mode == "control" || p.Deployment.Mode == "operator" {
		if p.Credentials != nil {
			return nil, errors.New("control/operator installer must not contain enrollment credentials")
		}
	} else {
		v := p.Credentials
		if v == nil || v.AuthKey == "" || v.EnrollmentId == "" || v.EnrollmentCode == "" || v.ControlKey == "" {
			return nil, errors.New("installer requires complete bootstrap credentials; generate a new installer from an authorized machine")
		}
		if v.ExpiresAt <= time.Now().Unix() {
			return nil, errors.New("bootstrap expired; generate a new installer")
		}
	}
	return &p, nil
}

func bundlePlacement(in *v1.DeploymentRequest, f Flags) error {
	for _, item := range []struct {
		path string
		dest *[]byte
	}{{f.PlacementFile, &in.PlacementJson}, {f.ForwarderPlacementFile, &in.ForwarderPlacementJson}} {
		if item.path == "" {
			continue
		}
		p, err := readPlacement(item.path)
		if err != nil {
			return err
		}
		*item.dest, err = json.Marshal(p)
		if err != nil {
			return err
		}
	}
	return nil
}
func applyBundlePlacement(o client.Object, in *v1.DeploymentRequest) error {
	if len(in.PlacementJson) > 0 {
		if err := json.Unmarshal(in.PlacementJson, &crSpec(o).Placement); err != nil {
			return err
		}
	}
	if len(in.ForwarderPlacementJson) > 0 {
		g, ok := o.(*funnel.FunnelGateway)
		if !ok {
			return fmt.Errorf("forwarder placement requires gateway")
		}
		if err := json.Unmarshal(in.ForwarderPlacementJson, &g.Spec.ForwarderPlacement); err != nil {
			return err
		}
	}
	return nil
}
