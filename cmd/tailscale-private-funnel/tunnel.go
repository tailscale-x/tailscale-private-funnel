package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
)

func tunnelCommand() *cobra.Command {
	var settings tunnel.Settings
	var output string
	c := &cobra.Command{Use: "tunnel <local-port>", Short: "Expose a loopback HTTP service through a foreground Tailscale tunnel", Long: "Run on Linux or macOS with an already-connected host Tailscale daemon. Phone home to private control for a one-use key and start a separate ephemeral identity; the host needs no role tag. No Docker, service installation or background daemon. --tailscale host instead reuses an already role-tagged host. Ctrl+C withdraws owned forwarding and queues DNS cleanup.", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		port, err := strconv.ParseUint(args[0], 10, 16)
		if err != nil || port == 0 {
			return errors.New("local port must be between 1 and 65535")
		}
		settings.Port = uint16(port)
		if output != "text" && output != "json" {
			return errors.New("--output must be text or json")
		}
		return app.RunTunnel(c.Context(), settings, func(e app.TunnelEvent) {
			if output == "json" {
				_ = json.NewEncoder(c.OutOrStdout()).Encode(e)
			} else {
				fmt.Fprintf(c.OutOrStdout(), "%s: %s", e.Phase, e.Message)
				if e.URL != "" {
					fmt.Fprintf(c.OutOrStdout(), " · %s", e.URL)
				}
				fmt.Fprintln(c.OutOrStdout())
			}
		})
	}}
	c.Flags().StringVar(&settings.Connection, "tailscale", "embedded", "embedded phone-home identity or already role-tagged host")
	c.Flags().StringVar(&settings.Hostname, "hostname", "", "public hostname permitted by control policy")
	c.Flags().StringVar(&settings.ControlURL, "control-url", "", "private control URL (or saved client.json)")
	c.Flags().StringVar(&settings.Installation, "installation", "main", "Funnel installation slug")
	c.Flags().StringVar(&settings.LocalAddress, "local-address", "127.0.0.1", "loopback backend IP (127.0.0.1 or ::1)")
	c.Flags().StringVar(&settings.Name, "name", "", "local tunnel handle (default derived from hostname, target and control)")
	c.Flags().StringVar(&settings.Socket, "tailscale-socket", "", "optional existing Tailscale LocalAPI socket")
	c.Flags().StringVar(&output, "output", "text", "progress events: text or newline-delimited json")
	_ = c.MarkFlagRequired("hostname")
	return c
}
