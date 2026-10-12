package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
)

var version = "dev"

func main() {
	app.BuildVersion = version
	root := &cobra.Command{
		Use:   "tailscale-private-funnel",
		Short: "Private funnel loadbalancer, ingress, and control service",
	}
	root.AddCommand(modeCommand("control"), modeCommand("loadbalancer"), modeCommand("ingress"), versionCommand())
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
		cmd.AddCommand(setup)
	}
	if mode == "loadbalancer" || mode == "ingress" {
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
		cmd.AddCommand(service)
		doctor := &cobra.Command{Use: "doctor", Short: "check deployment prerequisites", RunE: func(cmd *cobra.Command, _ []string) error {
			return app.Doctor(mode, opts)
		}}
		cmd.AddCommand(doctor)
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
	serve.Flags().StringSliceVar(&opts.LabelKeys, "label-key", nil, "additional local exposure label aliases")
	serve.Flags().StringSliceVar(&opts.DenyNetworks, "deny-network", nil, "local Docker network name or ID pattern to deny")
	serve.Flags().StringVar(&opts.ConfigFile, "config", "", "deployment configuration path")
	cmd.AddCommand(serve)
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
