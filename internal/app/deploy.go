package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type savedConfig struct {
	Installation        string `json:"installation"`
	TailscaleConnection string `json:"tailscale_connection"`
	TailscaleSocket     string `json:"tailscale_socket,omitempty"`

	Mode                 string   `json:"mode"`
	Hostname             string   `json:"hostname"`
	StateDir             string   `json:"state_dir"`
	DataDir              string   `json:"data_dir"`
	ManagementURL        string   `json:"management_url"`
	LoginServer          string   `json:"login_server"`
	AcceptDNS            bool     `json:"accept_dns"`
	Tags                 []string `json:"advertise_tags,omitempty"`
	Listen               string   `json:"listen"`
	CertEmail            string   `json:"cert_email,omitempty"`
	LoadBalancerHostname string   `json:"loadbalancer_hostname,omitempty"`
	LabelKeys            []string `json:"label_keys,omitempty"`
	DenyNetworks         []string `json:"deny_networks,omitempty"`
}

func configPath(mode string, o Options) string {
	if o.ConfigFile != "" {
		return o.ConfigFile
	}
	return filepath.Join(o.DataDir, "config.json")
}

func writeConfig(mode string, o Options) error {
	path := configPath(mode, o)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(savedConfig{Mode: mode, Hostname: o.Hostname, StateDir: o.StateDir, DataDir: o.DataDir, ManagementURL: o.ManagementURL, Installation: o.Installation, TailscaleConnection: o.TailscaleConnection, TailscaleSocket: o.TailscaleSocket, LoginServer: o.LoginServer, AcceptDNS: o.AcceptDNS, Tags: o.Tags, Listen: o.Listen, CertEmail: o.CertEmail, LoadBalancerHostname: o.LoadBalancerHostname, LabelKeys: o.LabelKeys, DenyNetworks: o.DenyNetworks}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadConfig(mode string, o Options) (Options, error) {
	path := o.ConfigFile
	if path == "" {
		path = configPath(mode, o)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && o.ConfigFile == "" {
			return o, nil
		}
		return o, err
	}
	var saved savedConfig
	if err := json.Unmarshal(b, &saved); err != nil {
		return o, fmt.Errorf("read config: %w", err)
	}
	if saved.Mode != "" && saved.Mode != mode {
		return o, fmt.Errorf("config mode is %q, expected %q", saved.Mode, mode)
	}
	o.Hostname, o.StateDir, o.DataDir = saved.Hostname, saved.StateDir, saved.DataDir
	o.ManagementURL = saved.ManagementURL
	o.Installation, o.TailscaleConnection, o.TailscaleSocket = saved.Installation, saved.TailscaleConnection, saved.TailscaleSocket
	o.LoginServer, o.AcceptDNS, o.Tags, o.Listen = saved.LoginServer, saved.AcceptDNS, saved.Tags, saved.Listen
	o.CertEmail, o.LoadBalancerHostname = saved.CertEmail, saved.LoadBalancerHostname
	o.LabelKeys, o.DenyNetworks = saved.LabelKeys, saved.DenyNetworks
	return o, nil
}

func Deploy(ctx context.Context, mode string, o Options) error {
	if mode != "loadbalancer" && !store.IsGateway(mode) {
		return errors.New("deploy is available for loadbalancer and gateway only")
	}
	o.NonInteractive = true
	var err error
	o, err = defaults(mode, o)
	if err != nil {
		return err
	}
	if u, parseErr := url.Parse(o.ManagementURL); parseErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("valid control URL required")
	}
	if mode == "docker-gateway" {
		if err := gatewayPreflight(); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(o.StateDir, 0700); err != nil {
		return err
	}
	if err := os.MkdirAll(o.DataDir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.DataDir, "hostname"), []byte(o.Hostname), 0600); err != nil {
		return err
	}
	if err := writeConfig(mode, o); err != nil {
		return err
	}
	o.BootstrapOnly = true
	if err := Serve(ctx, mode, o); err != nil {
		return fmt.Errorf("initial registration: %w", err)
	}
	return nil
}

func loadbalancerPreflight() error {
	if runtime.GOOS != "linux" {
		return errors.New("loadbalancer deploy requires Linux for ports 80/443 and systemd")
	}
	if _, err := exec.LookPath("ss"); err != nil {
		return errors.New("loadbalancer deploy requires the ss command to check ports 80/443")
	}
	for _, port := range []string{"80", "443"} {
		if out, err := exec.Command("sh", "-c", "command -v ss >/dev/null && ss -H -ltn '( sport = :"+port+" )' | grep -q .").CombinedOutput(); err == nil {
			return fmt.Errorf("TCP port %s is already in use: %s", port, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func gatewayPreflight() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("gateway deploy requires Docker CLI")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return errors.New("gateway deploy cannot access Docker; add the user to the docker group or fix the socket permissions")
	}
	return nil
}

// Preflight checks local dependencies without joining a tailnet or creating credentials.
func Preflight(mode string) error {
	switch mode {
	case "loadbalancer":
		return loadbalancerPreflight()
	case "docker-gateway":
		return gatewayPreflight()
	}
	return nil
}
