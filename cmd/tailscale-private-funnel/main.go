package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
)

var version = "dev"

func main() {
	app.BuildVersion = version
	root := &cobra.Command{
		Use:   "tailscale-private-funnel",
		Short: "Private funnel loadbalancer, gateway, and control service",
	}
	root.AddCommand(modeCommand("control"), modeCommand("loadbalancer"), modeCommand("docker-gateway"), modeCommand("tailnet-gateway"), modeCommand("k8s-gateway"), versionCommand())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func modeCommand(mode string) *cobra.Command {
	var opts app.Options
	cmd := &cobra.Command{Use: mode, Short: mode + " mode"}
	if mode == "docker-gateway" {
		var listen, target string
		relay := &cobra.Command{Use: "relay", Hidden: true}
		serve := &cobra.Command{Use: "serve", RunE: func(cmd *cobra.Command, _ []string) error { return gateway.ServeRelay(cmd.Context(), listen, target) }}
		serve.Flags().StringVar(&listen, "listen", ":10000", "relay listener")
		serve.Flags().StringVar(&target, "target", "", "backend address")
		check := &cobra.Command{Use: "check", RunE: func(cmd *cobra.Command, _ []string) error { return gateway.CheckRelay(target) }}
		check.Flags().StringVar(&target, "target", "", "backend address")
		relay.AddCommand(serve, check)
		cmd.AddCommand(relay)
	}
	if mode == "control" || mode == "loadbalancer" {
		kubernetes := &cobra.Command{Use: "kubernetes", Hidden: true}
		for _, action := range []string{"bootstrap", "serve", "import-state"} {
			var k app.Options
			var sourceState, sourceData string
			operation := action
			command := &cobra.Command{Use: action, RunE: func(cmd *cobra.Command, _ []string) error {
				switch operation {
				case "bootstrap":
					return app.BootstrapKubernetesNode(cmd.Context(), mode, k)
				case "serve":
					return app.ServeKubernetesNode(cmd.Context(), mode, k)
				default:
					return app.ImportKubernetesNode(cmd.Context(), mode, k, sourceState, sourceData)
				}
			}}
			command.Flags().StringVar(&k.KubeNamespace, "namespace", "", "Kubernetes namespace")
			command.Flags().StringVar(&k.IdentitySecret, "identity-secret", "", "precreated identity Secret")
			command.Flags().StringVar(&k.PersistentDir, "persistent-dir", "/persistent", "SQLite or Caddy PVC mount")
			command.Flags().StringVar(&k.BootstrapSecretDir, "bootstrap-secret-dir", "/bootstrap", "bootstrap Secret mount")
			command.Flags().StringVar(&k.Kubeconfig, "kubeconfig", "", "Kubernetes client configuration")
			command.Flags().StringVar(&k.StateDir, "state-dir", "", "runtime identity directory")
			command.Flags().StringVar(&k.DataDir, "data-dir", "", "runtime application directory")
			command.Flags().StringVar(&k.Hostname, "hostname", "", "bootstrap hostname")
			command.Flags().StringVar(&k.Tailnet, "tailnet", "", "bootstrap tailnet")
			command.Flags().StringVar(&k.KubernetesService, "kubernetes-service", "", "public Service name")
			command.Flags().StringVar(&k.ProbeListen, "probe-listen", ":9090", "health listener")
			command.Flags().StringVar(&sourceState, "source-state-dir", "", "stopped source tsnet state")
			command.Flags().StringVar(&sourceData, "source-data-dir", "", "stopped source application data")
			kubernetes.AddCommand(command)
		}
		cmd.AddCommand(kubernetes)
	}
	setup := &cobra.Command{Use: "join", Short: "bootstrap and run control using Tailscale OAuth", RunE: func(cmd *cobra.Command, _ []string) error {
		return app.Setup(cmd.Context(), mode, opts)
	}}
	setup.Flags().StringVar(&opts.Hostname, "hostname", "", "Tailscale hostname (defaults to the machine hostname)")
	setup.Flags().StringVar(&opts.Tailnet, "tailnet", "", "Tailscale tailnet name")
	setup.Flags().StringVar(&opts.OAuthClientIDFile, "oauth-client-id-file", "", "file containing the Tailscale OAuth client ID")
	setup.Flags().StringVar(&opts.OAuthClientSecretFile, "oauth-client-secret-file", "", "file containing the Tailscale OAuth client secret")
	setup.Flags().BoolVar(&opts.OAuthClientIDStdin, "oauth-client-id-stdin", false, "read the Tailscale OAuth client ID from stdin")
	setup.Flags().BoolVar(&opts.OAuthClientSecretStdin, "oauth-client-secret-stdin", false, "read the Tailscale OAuth client secret from stdin")
	setup.Flags().BoolVar(&opts.NonInteractive, "non-interactive", false, "do not open the setup wizard")
	setup.Flags().StringVar(&opts.StateDir, "state-dir", "", "persistent tsnet state directory")
	setup.Flags().StringVar(&opts.DataDir, "data-dir", "", "persistent application data directory")
	setup.Flags().StringVar(&opts.LoginServer, "login-server", "https://controlplane.tailscale.com", "Tailscale control server")
	setup.Flags().BoolVar(&opts.AcceptDNS, "accept-dns", true, "accept Tailscale DNS settings")
	setup.Flags().StringSliceVar(&opts.Tags, "advertise-tags", nil, "tags to advertise")
	if mode == "control" {
		setup.Flags().BoolVar(&opts.BootstrapOnly, "bootstrap-only", false, "join and persist identity without starting control")
		cmd.AddCommand(setup)
	}
	if mode != "control" {
		deploy := &cobra.Command{Use: "join", Short: "enroll, install, and start the native service", RunE: func(cmd *cobra.Command, _ []string) error {
			return app.Deploy(cmd.Context(), mode, opts)
		}}
		deploy.Flags().StringVar(&opts.Hostname, "hostname", "", "Tailscale hostname")
		deploy.Flags().StringVar(&opts.EnrollmentID, "enrollment-id", "", "control enrollment ID")
		deploy.Flags().StringVar(&opts.EnrollmentCode, "enrollment-code", "", "enrollment code (prefer --enrollment-code-file)")
		deploy.Flags().StringVar(&opts.EnrollmentCodeFile, "enrollment-code-file", "", "file containing the enrollment code")
		deploy.Flags().BoolVar(&opts.EnrollmentCodeStdin, "enrollment-code-stdin", false, "read the enrollment code from stdin")
		deploy.Flags().StringVar(&opts.ManagementURL, "management-url", "", "tailnet URL of the control service")
		deploy.Flags().StringVar(&opts.ControlKey, "control-key", "", "base64 control configuration verification key")
		deploy.Flags().StringVar(&opts.BootstrapURL, "bootstrap-url", "", "enrollment bootstrap URL")
		deploy.Flags().StringVar(&opts.StateDir, "state-dir", "", "persistent tsnet state directory")
		deploy.Flags().StringVar(&opts.DataDir, "data-dir", "", "persistent application data directory")
		deploy.Flags().StringVar(&opts.ConfigFile, "config", "", "writable deployment configuration path")
		deploy.Flags().StringVar(&opts.LoginServer, "login-server", "https://controlplane.tailscale.com", "Tailscale control server")
		deploy.Flags().BoolVar(&opts.AcceptDNS, "accept-dns", true, "accept Tailscale DNS settings")
		deploy.Flags().StringSliceVar(&opts.Tags, "advertise-tags", nil, "tags to advertise")
		deploy.Flags().StringVar(&opts.CertEmail, "cert-email", "", "ACME certificate contact email")
		if mode == "loadbalancer" {
			deploy.Flags().StringVar(&opts.LoadBalancerHostname, "loadbalancer-hostname", "", "public DNS hostname")
		}
		deploy.Flags().StringVar(&opts.BootstrapSecretDir, "bootstrap-secret-dir", "", "mounted Kubernetes enrollment Secret directory")
		deploy.Flags().BoolVar(&opts.InstallService, "install-service", mode != "k8s-gateway", "install and start systemd after enrollment")
		deploy.Flags().StringSliceVar(&opts.LabelKeys, "label-key", nil, "additional local exposure label aliases")
		deploy.Flags().StringSliceVar(&opts.DenyNetworks, "deny-network", nil, "local Docker network name or ID pattern to deny")
		cmd.AddCommand(deploy)
		service := &cobra.Command{Use: "service", Short: "manage the user systemd service"}
		service.PersistentFlags().StringVar(&opts.ConfigFile, "config", "", "deployment configuration path")
		service.PersistentFlags().StringVar(&opts.StateDir, "state-dir", "", "persistent tsnet state directory")
		service.PersistentFlags().StringVar(&opts.DataDir, "data-dir", "", "persistent application data directory")
		for _, action := range []string{"install", "start", "stop", "restart", "logs", "uninstall"} {
			action := action
			service.AddCommand(&cobra.Command{Use: action, RunE: func(cmd *cobra.Command, _ []string) error {
				opts.ServiceAction = action
				return app.Service(mode, action, opts)
			}})
		}
		if mode != "k8s-gateway" {
			cmd.AddCommand(service)
		}
		doctor := &cobra.Command{Use: "doctor", Short: "check deployment prerequisites", RunE: func(cmd *cobra.Command, _ []string) error {
			return app.Doctor(mode, opts)
		}}
		if mode != "k8s-gateway" {
			cmd.AddCommand(doctor)
		}
	}
	serve := &cobra.Command{Use: "serve", Short: "run the selected mode", RunE: func(cmd *cobra.Command, _ []string) error {
		return app.Serve(cmd.Context(), mode, opts)
	}}
	serve.Flags().StringVar(&opts.Hostname, "hostname", "", "Tailscale hostname (defaults to the machine hostname)")
	serve.Flags().StringVar(&opts.Tailnet, "tailnet", "", "Tailscale tailnet name")
	serve.Flags().StringVar(&opts.OAuthClientIDFile, "oauth-client-id-file", "", "file containing the Tailscale OAuth client ID")
	serve.Flags().BoolVar(&opts.OAuthClientIDStdin, "oauth-client-id-stdin", false, "read OAuth client ID from stdin")
	serve.Flags().BoolVar(&opts.OAuthClientSecretStdin, "oauth-client-secret-stdin", false, "read OAuth secret from stdin")
	serve.Flags().StringVar(&opts.OAuthClientSecretFile, "oauth-client-secret-file", "", "file containing the Tailscale OAuth client secret")
	serve.Flags().StringVar(&opts.StateDir, "state-dir", "", "persistent tsnet state directory")
	serve.Flags().StringVar(&opts.DataDir, "data-dir", "", "persistent application data directory")
	serve.Flags().StringVar(&opts.ManagementURL, "management-url", "", "tailnet URL of the control service")
	serve.Flags().StringVar(&opts.ControlKey, "control-key", "", "base64 control configuration verification key")
	serve.Flags().StringVar(&opts.Listen, "listen", "", "listen address")
	serve.Flags().StringVar(&opts.BootstrapListen, "bootstrap-listen", "", "optional non-tailnet listener for enrollment bootstrap")
	serve.Flags().StringVar(&opts.CertEmail, "cert-email", "", "ACME certificate contact email")
	if mode == "loadbalancer" {
		serve.Flags().StringVar(&opts.LoadBalancerHostname, "loadbalancer-hostname", "", "public DNS hostname")
	}
	if mode == "k8s-gateway" {
		serve.Flags().StringVar(&opts.Kubeconfig, "kubeconfig", "", "Kubernetes client config (default: in-cluster)")
		serve.Flags().StringVar(&opts.GatewayClass, "gateway-class", "tailscale-private-funnel", "GatewayClass to reconcile")
		serve.Flags().StringVar(&opts.IngressClass, "ingress-class", "tailscale-private-funnel", "IngressClass to reconcile")
		serve.Flags().StringSliceVar(&opts.WatchNamespaces, "watch-namespace", nil, "namespaces to watch (default: all)")
		serve.Flags().StringVar(&opts.ProbeListen, "probe-listen", ":9090", "local health listener")
	}
	serve.Flags().StringSliceVar(&opts.LabelKeys, "label-key", nil, "additional local exposure label aliases")
	serve.Flags().StringSliceVar(&opts.DenyNetworks, "deny-network", nil, "local Docker network name or ID pattern to deny")
	serve.Flags().StringVar(&opts.ConfigFile, "config", "", "deployment configuration path")
	serve.Flags().BoolVar(&opts.RequireJoined, "require-joined", false, "fail unless persistent bootstrap state exists")
	if mode == "control" || mode == "loadbalancer" {
		serve.Flags().StringVar(&opts.ProbeListen, "probe-listen", "", "local Kubernetes health probe listener")
	}
	if mode == "loadbalancer" {
		serve.Flags().StringVar(&opts.KubeNamespace, "kubernetes-namespace", "", "public Service namespace")
		serve.Flags().StringVar(&opts.KubernetesService, "kubernetes-service", "", "discover public addresses from this Service")
	}
	if mode == "k8s-gateway" {
		controller := &cobra.Command{Use: "controller", Short: "Kubernetes routing controller and identity broker"}
		serve = &cobra.Command{Use: "serve", Short: "run the Kubernetes controller and identity broker", RunE: func(cmd *cobra.Command, _ []string) error { return app.ServeKubernetesController(cmd.Context(), opts) }}
		serve.Flags().StringVar(&opts.Kubeconfig, "kubeconfig", "", "Kubernetes config (default: in-cluster)")
		serve.Flags().StringVar(&opts.GatewayClass, "gateway-class", "tailscale-private-funnel", "selected GatewayClass")
		serve.Flags().StringVar(&opts.IngressClass, "ingress-class", "tailscale-private-funnel", "selected IngressClass")
		serve.Flags().StringSliceVar(&opts.WatchNamespaces, "watch-namespace", nil, "namespaces to watch (default: all)")
		serve.Flags().StringVar(&opts.ProbeListen, "probe-listen", ":9090", "controller health listener")
		serve.Flags().StringVar(&opts.KubeNamespace, "namespace", "", "installation namespace")
		serve.Flags().StringVar(&opts.Installation, "installation", "", "Helm installation name")
		serve.Flags().StringVar(&opts.ControllerName, "controller-name", "tailscale-private-funnel.io/controller", "unique Gateway API controller identifier")
		controller.AddCommand(serve)
		var boot app.Options
		bootstrap := &cobra.Command{Use: "bootstrap", Hidden: true, RunE: func(cmd *cobra.Command, _ []string) error {
			return app.BootstrapKubernetesController(cmd.Context(), boot)
		}}
		bootstrap.Flags().StringVar(&boot.KubeNamespace, "namespace", "", "installation namespace")
		bootstrap.Flags().StringVar(&boot.Installation, "installation", "", "Helm installation name")
		bootstrap.Flags().StringVar(&boot.BootstrapSecretDir, "bootstrap-secret-dir", "/bootstrap", "mounted enrollment Secret")
		bootstrap.Flags().StringVar(&boot.StateDir, "state-dir", "/var/lib/tailscale-private-funnel/state", "temporary join state")
		bootstrap.Flags().StringVar(&boot.DataDir, "data-dir", "/var/lib/tailscale-private-funnel/data", "temporary join data")
		controller.AddCommand(bootstrap)
		cmd.AddCommand(controller)
		var forward app.Options
		forwarder := &cobra.Command{Use: "forwarder", Short: "Node-local Tailscale Service forwarding"}
		run := &cobra.Command{Use: "serve", RunE: func(cmd *cobra.Command, _ []string) error {
			return app.ServeKubernetesForwarder(cmd.Context(), forward)
		}}
		run.Flags().StringVar(&forward.BrokerURL, "broker-url", "", "HTTPS identity broker")
		run.Flags().StringVar(&forward.BrokerCAFile, "broker-ca-file", "/broker/ca.crt", "broker CA")
		run.Flags().StringVar(&forward.BrokerTokenFile, "broker-token-file", "/identity/token", "audience-bound pod token")
		forwarder.AddCommand(run)
		cmd.AddCommand(forwarder)
	} else {
		cmd.AddCommand(serve)
	}
	if mode == "k8s-gateway" {
		return cmd
	}
	status := &cobra.Command{Use: "status", Short: "show local node status", RunE: func(cmd *cobra.Command, _ []string) error {
		return app.Status(mode, opts)
	}}
	status.Flags().StringVar(&opts.StateDir, "state-dir", "", "persistent tsnet state directory")
	status.Flags().StringVar(&opts.DataDir, "data-dir", "", "persistent application data directory")
	status.Flags().StringVar(&opts.ConfigFile, "config", "", "deployment configuration path")
	cmd.AddCommand(status)
	reset := &cobra.Command{Use: "reset", Short: "remove local state", RunE: func(cmd *cobra.Command, _ []string) error {
		return app.Reset(mode, opts)
	}}
	reset.Flags().StringVar(&opts.StateDir, "state-dir", "", "persistent tsnet state directory")
	reset.Flags().StringVar(&opts.DataDir, "data-dir", "", "persistent application data directory")
	reset.Flags().BoolVar(&opts.Force, "force", false, "skip confirmation")
	cmd.AddCommand(reset)
	return cmd
}

func versionCommand() *cobra.Command {
	return &cobra.Command{Use: "version", Run: func(cmd *cobra.Command, _ []string) { fmt.Println(version) }}
}
