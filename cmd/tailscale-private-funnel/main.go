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
	root.AddCommand(native, kube, management, &cobra.Command{Use: "version", Args: cobra.NoArgs, Run: func(c *cobra.Command, _ []string) { fmt.Fprintln(c.OutOrStdout(), version) }})
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
	runtime.AddCommand(deploymentcli.Installer("native"))
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
				if p.Deployment == nil || p.Deployment.Mode != mode {
					return errors.New("matching bootstrap bundle required")
				}
				d, key := p.Deployment, p.Credentials
				o.ManagementURL = p.ManagementUrl
				o.Installation = d.Installation
				o.TailscaleConnection = d.TailscaleConnection
				o.TailscaleSocket = d.TailscaleSocket
				o.CertEmail = d.CertificateEmail
				o.LoadBalancerHostname = d.PublicHostname
				o.LabelKeys = d.LabelKeys
				o.DenyNetworks = d.DenyNetworks
				if key != nil {
					_ = os.Setenv("TS_AUTHKEY", key.AuthKey)
				}
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

		runtime.AddCommand(parent)
	}
	return runtime
}
func runtimeFlags(c *cobra.Command, o *app.Options) {
	c.Flags().StringVar(&o.TailscaleConnection, "tailscale", "embedded", "Tailscale connection: embedded or host")
	c.Flags().StringVar(&o.TailscaleSocket, "tailscale-socket", "", "existing host LocalAPI socket")
	c.Flags().StringVar(&o.Installation, "installation", "main", "installation role boundary")
	c.Flags().StringVar(&o.BootstrapURL, "bootstrap-url", "", "operator bootstrap endpoint")
	c.Flags().StringVar(&o.BootstrapTokenFile, "bootstrap-token-file", "", "projected Kubernetes bootstrap token")
	c.Flags().StringVar(&o.BootstrapCAFile, "bootstrap-ca-file", "", "operator bootstrap CA")
	c.Flags().StringVar(&o.ManagementURL, "control-url", "", "stable private control Service URL")

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
	c.Flags().StringVar(&o.UsersClientIDFile, "users-client-id-file", "", "protected users:read client ID file")
	c.Flags().StringVar(&o.UsersClientSecretFile, "users-client-secret-file", "", "protected users:read client secret file")
	c.Flags().StringVar(&o.ServicesClientIDFile, "services-client-id-file", "", "protected Service-management OAuth ID file")
	c.Flags().StringVar(&o.ServicesClientSecretFile, "services-client-secret-file", "", "protected Service-management OAuth secret file")
	c.Flags().StringVar(&o.LoadBalancerHostname, "public-hostname", "", "public DNS hostname")
	c.Flags().StringVar(&o.CertEmail, "certificate-email", "", "ACME contact email")
	c.Flags().StringVar(&o.Listen, "listen", "", "")
}
func kubernetesRuntime() *cobra.Command {
	runtime := &cobra.Command{Use: "runtime", Hidden: true}
	runtime.AddCommand(deploymentcli.Installer("kubernetes"))
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
		actions := []string{"serve"}

		for _, action := range actions {
			action := action
			o := app.Options{}
			var cleanup bool
			command := &cobra.Command{Use: action, Hidden: true, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {

				if mode == "gateway" {
					if cleanup {
						return app.CleanupKubernetesAgent(c.Context(), o)
					}
					return app.ServeKubernetesAgent(c.Context(), o)
				}
				if cleanup {
					return app.CleanupKubernetesNode(c.Context(), mode, o)
				}
				return app.ServeKubernetesNode(c.Context(), mode, o)
			}}
			runtimeFlags(command, &o)
			command.Flags().StringVar(&o.KubeNamespace, "namespace", "", "")
			command.Flags().StringVar(&o.PersistentDir, "persistent-dir", "/persistent", "")
			command.Flags().StringVar(&o.KubernetesService, "kubernetes-service", "", "")
			command.Flags().StringVar(&o.MaterialDir, "material-dir", "/etc/funnel-material", "control encryption material")
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
	runtimeFlags(run, &o)
	run.Flags().StringVar(&o.KubeNamespace, "namespace", "", "")
	run.Flags().StringVar(&o.BrokerURL, "broker-url", "", "")
	run.Flags().StringVar(&o.BrokerCAFile, "broker-ca-file", "/broker/ca.crt", "")
	run.Flags().StringVar(&o.BrokerTokenFile, "broker-token-file", "/identity/token", "")
	run.Flags().StringVar(&o.ProbeListen, "probe-listen", ":9091", "")
	forwarder.AddCommand(run)
	runtime.AddCommand(forwarder)
	return runtime
}
