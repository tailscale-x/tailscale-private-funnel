package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	"github.com/tailscale-x/tailscale-private-funnel/internal/deploymentcli"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/manage"
	"github.com/tailscale-x/tailscale-private-funnel/internal/operator"
)

var version = "dev"

func main() {
	app.BuildVersion = version
	root := NewRoot(version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := root.ExecuteContext(ctx); e != nil {
		var forwarded *deploymentcli.ForwardedExitError
		if errors.As(e, &forwarded) {
			os.Exit(forwarded.Code)
		}
		format := "table"
		if c, _, x := root.Find(os.Args[1:]); x == nil {
			if f := c.Flag("output"); f != nil {
				format = f.Value.String()
			}
		}
		manage.WriteError(os.Stderr, e, format)
		os.Exit(manage.ExitCode(e))
	}
}
func NewRoot(version string) *cobra.Command {
	root := &cobra.Command{Use: "tailscale-private-funnel", Short: "Policy-controlled private Funnel deployments and management", SilenceErrors: true, SilenceUsage: true}
	management := &cobra.Command{Use: "manage", Short: "Manage an installation through the Tailscale-authorized Funnel API"}
	manage.Attach(management, nil)
	native := deploymentcli.Native(version)
	kube := deploymentcli.Kubernetes(version)
	native.AddCommand(nativeRuntime())
	kube.AddCommand(kubernetesRuntime())
	root.AddCommand(native, kube, management, deploymentcli.TransferRemote(version), &cobra.Command{Use: "version", Args: cobra.NoArgs, Run: func(c *cobra.Command, _ []string) { fmt.Fprintln(c.OutOrStdout(), version) }})
	// Groups must reject unknown commands rather than silently printing help.
	var strict func(*cobra.Command)
	strict = func(c *cobra.Command) {
		if c.HasSubCommands() && c.Run == nil && c.RunE == nil {
			c.Args = cobra.NoArgs
			c.RunE = func(c *cobra.Command, _ []string) error {
				return fmt.Errorf("%s requires a subcommand", c.CommandPath())
			}
		}
		for _, child := range c.Commands() {
			strict(child)
		}
	}
	strict(root)
	return root
}
func nativeRuntime() *cobra.Command {
	runtime := &cobra.Command{Use: "runtime", Hidden: true}
	runtime.AddCommand(deploymentcli.Installer("native"), deploymentcli.TransferInstaller("native"))
	for _, mode := range []string{"control", "loadbalancer", "docker-gateway", "tailnet-gateway"} {
		mode := mode
		o := app.Options{}
		var bootstrap, cleanup, stdin bool
		parent := &cobra.Command{Use: mode, Hidden: true}
		serve := &cobra.Command{Use: "serve", Hidden: true, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			o.RequireJoined = !bootstrap && !stdin
			if cleanup {
				return app.Cleanup(c.Context(), mode, o)
			}
			if stdin {
				b, e := io.ReadAll(io.LimitReader(c.InOrStdin(), 2<<20))
				if e != nil {
					return e
				}
				var p v1.DeploymentInstructions
				if e = json.Unmarshal(b, &p); e != nil {
					return e
				}
				if p.Credentials == nil || p.Deployment == nil || p.Deployment.Mode != mode {
					return errors.New("matching bootstrap bundle required")
				}
				d, key := p.Deployment, p.Credentials
				o.ManagementURL = p.ManagementUrl
				o.EnrollmentID = key.EnrollmentId
				o.EnrollmentCode = key.EnrollmentCode
				o.ControlKey = key.ControlKey
				o.CertEmail = d.CertificateEmail
				o.LoadBalancerHostname = d.PublicHostname
				o.LabelKeys = d.LabelKeys
				o.DenyNetworks = d.DenyNetworks
				_ = os.Setenv("TS_AUTHKEY", key.AuthKey)
				defer os.Unsetenv("TS_AUTHKEY")
				return app.Deploy(c.Context(), mode, o)
			}
			o.BootstrapOnly = bootstrap
			return app.Serve(c.Context(), mode, o)
		}}
		serve.Flags().BoolVar(&bootstrap, "bootstrap-only", false, "initialize persistent state then exit")
		serve.Flags().BoolVar(&cleanup, "cleanup-only", false, "revoke remote resources without removing durable files")
		serve.Flags().BoolVar(&stdin, "bootstrap-stdin", false, "read one-use bootstrap bundle")
		runtimeFlags(serve, &o)
		parent.AddCommand(serve)
		if mode == "docker-gateway" {
			relay := &cobra.Command{Use: "relay", Hidden: true}
			var listen, target string
			run := &cobra.Command{Use: "serve", Hidden: true, RunE: func(c *cobra.Command, _ []string) error { return gateway.ServeRelay(c.Context(), listen, target) }}
			run.Flags().StringVar(&listen, "listen", ":10000", "")
			run.Flags().StringVar(&target, "target", "", "")
			var checkTarget string
			check := &cobra.Command{Use: "check", Hidden: true, RunE: func(c *cobra.Command, _ []string) error { return gateway.CheckRelay(checkTarget) }}
			check.Flags().StringVar(&checkTarget, "target", "", "")
			relay.AddCommand(run, check)
			parent.AddCommand(relay)
		}
		runtime.AddCommand(parent)
	}
	return runtime
}
func runtimeFlags(c *cobra.Command, o *app.Options) {
	c.Flags().StringVar(&o.Database.Type, "database", "sqlite", "")
	c.Flags().StringVar(&o.Database.URLFile, "database-url-file", "", "")
	c.Flags().StringVar(&o.Database.CAFile, "database-ca-file", "", "")
	c.Flags().StringVar(&o.Database.TLSMode, "database-tls-mode", "", "")
	c.Flags().StringVar(&o.Hostname, "hostname", "", "")
	c.Flags().StringVar(&o.StateDir, "state-dir", "", "")
	c.Flags().StringVar(&o.DataDir, "data-dir", "", "")
	c.Flags().StringVar(&o.ConfigFile, "config", "", "")
	c.Flags().StringArrayVar(&o.Administrators, "administrator", nil, "")
	c.Flags().StringVar(&o.Tailnet, "tailnet", "", "")
	c.Flags().StringVar(&o.OAuthClientIDFile, "oauth-client-id-file", "", "")
	c.Flags().StringVar(&o.OAuthClientSecretFile, "oauth-client-secret-file", "", "")
	c.Flags().StringVar(&o.Listen, "listen", "", "")
}
func kubernetesRuntime() *cobra.Command {
	runtime := &cobra.Command{Use: "runtime", Hidden: true}
	runtime.AddCommand(deploymentcli.Installer("kubernetes"), deploymentcli.TransferInstaller("kubernetes"))
	var namespaces []string
	var probe string
	op := &cobra.Command{Use: "operator", Hidden: true}
	run := &cobra.Command{Use: "serve", Hidden: true, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return operator.Run(c.Context(), namespaces, probe) }}
	run.Flags().StringArrayVar(&namespaces, "managed-namespace", nil, "")
	run.Flags().StringVar(&probe, "probe-listen", ":9090", "")
	op.AddCommand(run)
	runtime.AddCommand(op)
	for _, mode := range []string{"control", "loadbalancer", "gateway"} {
		mode := mode
		parent := &cobra.Command{Use: mode, Hidden: true}
		actions := []string{"bootstrap", "serve"}
		if mode == "control" {
			actions = append(actions, "transfer-hold", "transfer-export", "transfer-mark", "transfer-restore")
		}
		for _, action := range actions {
			action := action
			o := app.Options{}
			var cleanup bool
			var transferID string
			command := &cobra.Command{Use: action, Hidden: true, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
				switch action {
				case "transfer-hold":
					<-c.Context().Done()
					return nil
				case "transfer-export":
					b, err := app.ExportKubernetesControl(c.Context(), o)
					if err != nil {
						return err
					}
					return json.NewEncoder(c.OutOrStdout()).Encode(b)
				case "transfer-mark":
					return app.MarkKubernetesControlTransferred(c.Context(), o, transferID)
				case "transfer-restore":
					var b app.ControlTransfer
					d := json.NewDecoder(io.LimitReader(c.InOrStdin(), 512<<20))
					d.DisallowUnknownFields()
					if err := d.Decode(&b); err != nil {
						return errors.New("invalid control transfer input")
					}
					return app.RestoreKubernetesControl(c.Context(), o, &b)
				}
				if mode == "gateway" {
					if cleanup {
						return app.CleanupKubernetesAgent(c.Context(), o)
					}
					if action == "bootstrap" {
						return app.BootstrapKubernetesAgent(c.Context(), o)
					}
					return app.ServeKubernetesAgent(c.Context(), o)
				}
				if cleanup {
					return app.CleanupKubernetesNode(c.Context(), mode, o)
				}
				if action == "bootstrap" {
					return app.BootstrapKubernetesNode(c.Context(), mode, o)
				}
				return app.ServeKubernetesNode(c.Context(), mode, o)
			}}
			runtimeFlags(command, &o)
			command.Flags().StringVar(&transferID, "transfer-id", "", "")
			command.Flags().StringVar(&o.KubeNamespace, "namespace", "", "")
			command.Flags().StringVar(&o.Installation, "installation", "", "")
			command.Flags().StringVar(&o.IdentitySecret, "identity-secret", "", "")
			command.Flags().StringVar(&o.PersistentDir, "persistent-dir", "/persistent", "")
			command.Flags().StringVar(&o.BootstrapSecretDir, "bootstrap-secret-dir", "/bootstrap", "")
			command.Flags().StringVar(&o.KubernetesService, "kubernetes-service", "", "")
			command.Flags().StringVar(&o.GatewayClass, "gateway-class", "tailscale-private-funnel", "")
			command.Flags().StringVar(&o.IngressClass, "ingress-class", "tailscale-private-funnel", "")
			command.Flags().StringVar(&o.ControllerName, "agent-name", "tailscale-private-funnel.io/gateway-agent", "")
			command.Flags().StringArrayVar(&o.WatchNamespaces, "watch-namespace", nil, "")
			command.Flags().StringVar(&o.ProbeListen, "probe-listen", ":9090", "")
			command.Flags().BoolVar(&cleanup, "cleanup-only", false, "")
			parent.AddCommand(command)
		}
		runtime.AddCommand(parent)
	}
	forwarder := &cobra.Command{Use: "forwarder", Hidden: true}
	var o app.Options
	run = &cobra.Command{Use: "serve", Hidden: true, RunE: func(c *cobra.Command, _ []string) error { return app.ServeKubernetesForwarder(c.Context(), o) }}
	run.Flags().StringVar(&o.BrokerURL, "broker-url", "", "")
	run.Flags().StringVar(&o.BrokerCAFile, "broker-ca-file", "/broker/ca.crt", "")
	run.Flags().StringVar(&o.BrokerTokenFile, "broker-token-file", "/identity/token", "")
	run.Flags().StringVar(&o.ProbeListen, "probe-listen", ":9091", "")
	forwarder.AddCommand(run)
	runtime.AddCommand(forwarder)
	return runtime
}
