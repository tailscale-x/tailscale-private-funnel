package deploymentcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/manage"
	"github.com/tailscale-x/tailscale-private-funnel/internal/native"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http"
)

type Flags struct {
	StorageClass, StorageSize, PlacementFile, ForwarderPlacementFile, Installation, ConfigFile, Type, Output, Version, DisplayName, Hostname, ControlURL, Namespace, Context, Kubeconfig, Tailnet, StateDir, DataDir, OAuthID, OAuthSecret, PublicHostname, Email, GatewayClass, IngressClass string
	Wait, DryRun, Yes, BootstrapStdin, NonInteractive                                                                                                                                                                                                                                         bool
	Timeout                                                                                                                                                                                                                                                                                   time.Duration
	NodeSelector                                                                                                                                                                                                                                                                              map[string]string
	Administrators, WatchNamespaces, ManagedNamespaces, LabelKeys, DenyNetworks                                                                                                                                                                                                               []string
}

func common(c *cobra.Command, f *Flags, version string) {
	c.PreRunE = func(c *cobra.Command, _ []string) error {
		f.DisplayName = strings.TrimSpace(f.DisplayName)
		if len([]rune(f.DisplayName)) > 128 {
			return errors.New("display name exceeds 128 characters")
		}
		if f.Timeout <= 0 {
			return errors.New("--timeout must be positive")
		}
		if (c.Name() == "create" || c.Name() == "create-remote" || c.Name() == "update") && !provision.ValidVersion(f.Version) {
			return errors.New("--version requires a pinned release or dev version")
		}
		if c.Name() == "create-remote" {
			if f.Output != "instructions" && f.Output != "script" && f.Output != "json" {
				return errors.New("--output must be instructions, script, or json")
			}
		} else if f.Output != "table" && f.Output != "json" {
			return errors.New("--output must be table or json")
		}
		return nil
	}

	c.Flags().StringVar(&f.Output, "output", "table", "output: table or json")
	c.Flags().BoolVar(&f.DryRun, "dry-run", false, "validate and render without mutation")
	c.Flags().BoolVar(&f.Wait, "wait", false, "wait for reconciliation and readiness")
	c.Flags().DurationVar(&f.Timeout, "timeout", 10*time.Minute, "operation timeout")
	c.Flags().StringVar(&f.Version, "version", version, "pinned component version")
	c.Flags().StringVar(&f.DisplayName, "display-name", "", "editable display label")
	c.Flags().StringVar(&f.Hostname, "hostname", "", "Tailscale hostname; defaults to resource name")
	c.Flags().StringVar(&f.ControlURL, "control-url", "", "Funnel control URL")
}
func creation(c *cobra.Command, f *Flags, kind string, kube bool) {
	if kube {
		if kind == "controls" || kind == "loadbalancers" {
			c.Flags().StringVar(&f.StorageClass, "storage-class", "", "PVC StorageClass; omit only when the cluster has a default")
			c.Flags().StringVar(&f.StorageSize, "storage-size", "1Gi", "persistent volume size")
		}
		c.Flags().StringVar(&f.PlacementFile, "placement-file", "", "component Pod placement JSON (nodeSelector, affinity, tolerations)")
		if kind == "gateways" {
			c.Flags().StringVar(&f.ForwarderPlacementFile, "forwarder-placement-file", "", "forwarder Pod placement JSON")
		}
		c.Flags().StringVar(&f.Installation, "installation", "main", "installation trust boundary")
		c.Flags().StringToStringVar(&f.NodeSelector, "node-selector", nil, "component placement (gateway: forwarders); key=value pairs")
	}
	if kind == "gateways" && !kube {
		c.Flags().StringVar(&f.Type, "type", "", "gateway type: docker or tailnet")
	}
	if kind == "controls" {
		c.Flags().StringArrayVar(&f.Administrators, "administrator", nil, "additional first-bootstrap administrator selector; default autogroup:owner remains")
		c.Flags().StringVar(&f.Tailnet, "tailnet", "", "Tailscale tailnet name")
		c.Flags().StringVar(&f.OAuthID, "oauth-client-id-file", "", "protected OAuth client ID file")
		c.Flags().StringVar(&f.OAuthSecret, "oauth-client-secret-file", "", "protected OAuth client secret file")
		c.Flags().BoolVar(&f.NonInteractive, "non-interactive", false, "require scripted bootstrap inputs")
	}
	if kind == "loadbalancers" {
		c.Flags().StringVar(&f.PublicHostname, "public-hostname", "", "public DNS hostname")
		c.Flags().StringVar(&f.Email, "certificate-email", "", "ACME contact email")
	}
	if kind == "gateways" && kube {
		c.Flags().StringVar(&f.GatewayClass, "gateway-class", "", "GatewayClass name")
		c.Flags().StringVar(&f.IngressClass, "ingress-class", "", "IngressClass name")
		c.Flags().StringArrayVar(&f.WatchNamespaces, "watch-namespace", nil, "routing namespace; repeatable")
	}
	if !kube {
		c.Flags().StringVar(&f.StateDir, "state-dir", "", "persistent identity path")
		c.Flags().StringVar(&f.DataDir, "data-dir", "", "persistent application path")
		if kind != "controls" {
			c.Flags().BoolVar(&f.BootstrapStdin, "bootstrap-stdin", false, "read one-use enrollment bundle from stdin")
		}
	}
	if kind == "gateways" && !kube {
		c.Flags().StringArrayVar(&f.LabelKeys, "label-key", nil, "additional Docker label alias")
		c.Flags().StringArrayVar(&f.DenyNetworks, "deny-network", nil, "local Docker network deny pattern")
	}
}
func request(kind, name string, f Flags, target string) (*v1.DeploymentRequest, error) {
	mode := "control"
	if kind == "loadbalancers" {
		mode = "loadbalancer"
	}
	if kind == "gateways" {
		if target == "kubernetes" {
			mode = "k8s-gateway"
		} else {
			if f.Type != "docker" && f.Type != "tailnet" {
				return nil, errors.New("--type docker or tailnet is required")
			}
			mode = f.Type + "-gateway"
		}
	}
	if f.Hostname == "" {
		f.Hostname = name
	}
	return &v1.DeploymentRequest{Mode: mode, Target: target, StorageClass: f.StorageClass, StorageSize: f.StorageSize, Administrators: f.Administrators, NodeSelector: f.NodeSelector, Installation: f.Installation, Wait: f.Wait, TimeoutSeconds: uint32(f.Timeout.Seconds()), ResourceName: name, Name: name, DisplayName: f.DisplayName, Hostname: f.Hostname, Version: f.Version, StateDir: f.StateDir, DataDir: f.DataDir, Tailnet: f.Tailnet, OauthClientIdFile: f.OAuthID, OauthClientSecretFile: f.OAuthSecret, PublicHostname: f.PublicHostname, CertificateEmail: f.Email, Namespace: f.Namespace, Context: f.Context, GatewayClass: f.GatewayClass, IngressClass: f.IngressClass, WatchNamespaces: f.WatchNamespaces, LabelKeys: f.LabelKeys, DenyNetworks: f.DenyNetworks}, nil
}
func apiClient(url string) (funnelv1connect.DeploymentServiceClient, error) {
	base, e := manage.ResolveURL(url)
	if e != nil {
		return nil, e
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return funnelv1connect.NewDeploymentServiceClient(&http.Client{Transport: transport}, base), nil
}
func output(c *cobra.Command, value any, format string) error {
	if format == "json" {
		e := json.NewEncoder(c.OutOrStdout())
		e.SetIndent("", "  ")
		return e.Encode(value)
	}
	b, e := json.MarshalIndent(value, "", "  ")
	if e == nil {
		_, e = fmt.Fprintln(c.OutOrStdout(), string(b))
	}
	return e
}

type ForwardedExitError struct{ Code int }

func (e *ForwardedExitError) Error() string { return "target command already reported its error" }
func elevate(c *cobra.Command, f Flags) (bool, error) {
	if os.Geteuid() == 0 || f.DryRun {
		return false, nil
	}
	exe, e := os.Executable()
	if e != nil {
		return true, e
	}
	args := append([]string{"-n", "--", exe}, os.Args[1:]...)
	cmd := exec.CommandContext(c.Context(), "sudo", args...)
	cmd.Stdin = c.InOrStdin()
	cmd.Stdout = c.OutOrStdout()
	cmd.Stderr = c.ErrOrStderr()
	if err := cmd.Run(); err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) {
			return true, &ForwardedExitError{Code: exited.ExitCode()}
		}
		return true, err
	}
	return true, nil
}
func Native(version string) *cobra.Command {
	root := &cobra.Command{Use: "native", Short: "Manage named services on this Linux machine"}
	for _, kind := range []string{"controls", "gateways", "loadbalancers"} {
		parent := &cobra.Command{Use: kind}
		root.AddCommand(parent)
		for _, action := range []string{"create", "create-remote", "list", "get", "update", "delete", "start", "stop", "restart", "logs"} {
			kind, action := kind, action
			f := Flags{}
			use := action + " NAME"
			args := cobra.ExactArgs(1)
			if action == "list" {
				use = action
				args = cobra.NoArgs
			}
			c := &cobra.Command{Use: use, Short: action + " a native " + kind + " resource", Args: args}
			common(c, &f, version)
			if action == "create" || action == "create-remote" {
				creation(c, &f, kind, false)
			}
			if action == "create-remote" {
				f.Output = "instructions"
				c.Flags().Lookup("output").DefValue = "instructions"
			}
			if action == "delete" {
				c.Flags().BoolVar(&f.Yes, "yes", false, "confirm revocation and removal of all owned state")
			}
			c.RunE = func(c *cobra.Command, args []string) error {
				ctx, cancel := context.WithTimeout(c.Context(), f.Timeout)
				defer cancel()
				name := ""
				if len(args) > 0 {
					name = args[0]
					if e := native.ValidName(name); e != nil {
						return e
					}
				}
				if action == "delete" && !f.Yes && !f.DryRun {
					return errors.New("delete requires --yes")
				}
				mutates := action != "create-remote"
				if mutates {
					if handled, e := elevate(c, f); handled {
						return e
					}
				}
				manager := native.Default()
				switch action {
				case "create", "create-remote":
					in, e := request(kind, name, f, "native")
					if e != nil {
						return e
					}
					plan, e := provision.Resolve(in, f.ControlURL, f.Version)
					if e != nil {
						return e
					}
					if action == "create-remote" {
						if kind != "controls" && !f.DryRun {
							rpc, e := apiClient(f.ControlURL)
							if e != nil {
								return e
							}
							res, e := rpc.Authorize(ctx, connect.NewRequest(in))
							if e != nil {
								return e
							}
							plan = res.Msg
						}
						rendered, e := provision.RemoteOutput(plan, f.Output, f.DryRun)
						if e != nil {
							return e
						}
						_, e = io.WriteString(c.OutOrStdout(), rendered)
						return e
					}
					resource, e := manager.Resolve(kind, name, in.Mode, f.DisplayName, in.Hostname, f.Version, f.StateDir, f.DataDir)
					if e != nil {
						return e
					}
					resource.Tailnet = f.Tailnet
					if f.DryRun {
						return output(c, map[string]any{"dry_run": true, "resource": resource, "commands": plan.Commands}, f.Output)
					}
					if e = app.Preflight(in.Mode); e != nil {
						return e
					}
					if kind == "controls" {
						if f.OAuthID == "" || f.OAuthSecret == "" || f.Tailnet == "" {
							return errors.New("control creation requires --tailnet and protected OAuth files; create a tagged OAuth client with auth_keys, devices:core, services and users:read")
						}
						for _, path := range []string{f.OAuthID, f.OAuthSecret} {
							if _, e = privateFile(path); e != nil {
								return e
							}
						}
					}
					in.StateDir = resource.StateDir
					in.DataDir = resource.DataDir
					if kind != "controls" {
						if f.BootstrapStdin {
							b, e := io.ReadAll(io.LimitReader(c.InOrStdin(), 2<<20))
							if e != nil {
								return e
							}
							var bundle v1.DeploymentInstructions
							if e = protojson.Unmarshal(b, &bundle); e != nil {
								return e
							}
							if bundle.Deployment == nil || bundle.Deployment.Mode != in.Mode || bundle.Deployment.ResourceName != name {
								return errors.New("bootstrap resource binding mismatch")
							}
							plan = &bundle
						} else {
							rpc, e := apiClient(f.ControlURL)
							if e != nil {
								return e
							}
							res, e := rpc.Authorize(ctx, connect.NewRequest(in))
							if e != nil {
								return e
							}
							plan = res.Msg
						}
					}
					executable, e := verifiedBinary(ctx, f.Version)
					if e != nil {
						return e
					}
					manager.Binary = executable
					oauth := []string{}
					if kind == "controls" {
						if f.OAuthID == "" || f.OAuthSecret == "" {
							return errors.New("control creation requires protected OAuth credential files")
						}
						oauth = []string{"--oauth-client-id-file", f.OAuthID, "--oauth-client-secret-file", f.OAuthSecret}
						for _, selector := range f.Administrators {
							oauth = append(oauth, "--administrator", selector)
						}
					}
					resource, e = manager.Create(ctx, resource, plan, oauth)
					if e != nil {
						return e
					}
					if f.Wait {
						resource, e = manager.Wait(ctx, kind, name)
						if e != nil {
							return e
						}
					}
					return output(c, resource, f.Output)
				case "list":
					v, e := manager.List(ctx, kind)
					if e != nil {
						return e
					}
					return output(c, v, f.Output)
				case "get":
					v, e := manager.Get(ctx, kind, name)
					if e == nil && f.Wait {
						v, e = manager.Wait(ctx, kind, name)
					}
					if e != nil {
						return e
					}
					return output(c, v, f.Output)
				case "logs":
					b, e := manager.Logs(ctx, kind, name)
					if e != nil {
						return e
					}
					_, e = c.OutOrStdout().Write(b)
					return e
				default:
					if f.DryRun {
						return output(c, map[string]any{"dry_run": true, "action": action, "kind": kind, "name": name}, f.Output)
					}
					if action == "delete" {
						if e := manager.Delete(ctx, kind, name); e != nil {
							return e
						}
						return output(c, map[string]any{"deleted": true, "name": name}, f.Output)
					}
					var r native.Resource
					var e error
					if action == "update" {
						manager.Binary, e = verifiedBinary(ctx, f.Version)
						if e != nil {
							return e
						}
						r, e = manager.Update(ctx, kind, name, f.Version, f.DisplayName)
					} else {
						r, e = manager.Service(ctx, kind, name, action)
					}
					if e != nil {
						return e
					}
					if f.Wait && action != "stop" {
						r, e = manager.Wait(ctx, kind, name)
						if e != nil {
							return e
						}
					}
					return output(c, r, f.Output)
				}
			}
			parent.AddCommand(c)
		}
	}
	return root
}
func verifiedBinary(ctx context.Context, version string) (string, error) {
	if version == "dev" || version == "" {
		return "", errors.New("a pinned --version is required")
	}
	exe, e := os.Executable()
	if e != nil {
		return "", e
	}
	// The invoking prebuilt artifact already carries its version; installations still copy it into a root-owned version directory.
	if app.BuildVersion == version {
		return exe, nil
	}
	dir, e := os.MkdirTemp("", "funnel-binary-")
	if e != nil {
		return "", e
	}
	return (&provision.Executor{}).DownloadBinary(ctx, version, provision.ImageRepository+":"+version, dir)
}
