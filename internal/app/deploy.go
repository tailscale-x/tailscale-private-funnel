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
)

type savedConfig struct {
	Mode                 string   `json:"mode"`
	Hostname             string   `json:"hostname"`
	StateDir             string   `json:"state_dir"`
	DataDir              string   `json:"data_dir"`
	ManagementURL        string   `json:"management_url"`
	ControlKey           string   `json:"control_key,omitempty"`
	BootstrapURL         string   `json:"bootstrap_url"`
	EnrollmentID         string   `json:"enrollment_id"`
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
	b, err := json.MarshalIndent(savedConfig{Mode: mode, Hostname: o.Hostname, StateDir: o.StateDir, DataDir: o.DataDir, ManagementURL: o.ManagementURL, ControlKey: o.ControlKey, BootstrapURL: o.BootstrapURL, EnrollmentID: o.EnrollmentID, LoginServer: o.LoginServer, AcceptDNS: o.AcceptDNS, Tags: o.Tags, Listen: o.Listen, CertEmail: o.CertEmail, LoadBalancerHostname: o.LoadBalancerHostname, LabelKeys: o.LabelKeys, DenyNetworks: o.DenyNetworks}, "", "  ")
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
	o.ManagementURL, o.BootstrapURL, o.EnrollmentID = saved.ManagementURL, saved.BootstrapURL, saved.EnrollmentID
	o.ControlKey = saved.ControlKey
	o.LoginServer, o.AcceptDNS, o.Tags, o.Listen = saved.LoginServer, saved.AcceptDNS, saved.Tags, saved.Listen
	o.CertEmail, o.LoadBalancerHostname = saved.CertEmail, saved.LoadBalancerHostname
	o.LabelKeys, o.DenyNetworks = saved.LabelKeys, saved.DenyNetworks
	return o, nil
}

func readEnrollmentCode(o Options) (string, error) {
	if o.EnrollmentCode != "" {
		return strings.TrimSpace(o.EnrollmentCode), nil
	}
	if o.EnrollmentCodeFile != "" {
		b, err := os.ReadFile(o.EnrollmentCodeFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	if o.EnrollmentCodeStdin {
		b, err := os.ReadFile("/dev/stdin")
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", errors.New("an enrollment code is required via --enrollment-code-file or --enrollment-code-stdin")
}

func Deploy(ctx context.Context, mode string, o Options) error {
	if mode != "loadbalancer" && mode != "ingress" {
		return errors.New("deploy is available for loadbalancer and ingress only")
	}
	o.NonInteractive = true
	var err error
	o, err = defaults(mode, o)
	if err != nil {
		return err
	}
	if u, parseErr := url.Parse(o.ManagementURL); parseErr != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("a valid --management-url is required")
	}
	code, err := readEnrollmentCode(o)
	if err != nil {
		return err
	}
	if code == "" {
		return errors.New("enrollment code is empty")
	}
	o.EnrollmentCode = code
	if mode == "loadbalancer" {
		if err := loadbalancerPreflight(); err != nil {
			return err
		}
	} else if err := ingressPreflight(); err != nil {
		return err
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
	if err := os.WriteFile(filepath.Join(o.DataDir, "enrollment-code"), []byte(o.EnrollmentCode), 0600); err != nil {
		return err
	}
	if err := writeConfig(mode, o); err != nil {
		return err
	}
	o.BootstrapOnly = true
	if err := Serve(ctx, mode, o); err != nil {
		return fmt.Errorf("initial enrollment: %w", err)
	}
	if err := installService(mode, o); err != nil {
		return err
	}
	fmt.Printf("%s deployed and started as %s\n", mode, serviceName(mode))
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

func ingressPreflight() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("ingress deploy requires Docker CLI")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		return errors.New("ingress deploy cannot access Docker; add the user to the docker group or fix the socket permissions")
	}
	return nil
}

func serviceName(mode string) string { return "tailscale-private-funnel-" + mode }

func installService(mode string, o Options) error {
	if os.Geteuid() != 0 {
		return errors.New("installing a system service requires root; run the generated sudo command")
	}
	unitDir := "/etc/systemd/system"
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{mode, "serve", "--config", configPath(mode, o)}
	unit := "[Unit]\nDescription=Tailscale Private Funnel " + mode + "\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nExecStart=" + systemdQuote(exe)
	for _, arg := range args {
		unit += " " + systemdQuote(arg)
	}
	unit += "\nEnvironment=HOME=" + systemdQuote(o.DataDir) + "\nEnvironment=XDG_CONFIG_HOME=" + systemdQuote(filepath.Join(o.DataDir, "config")) + "\nEnvironment=XDG_DATA_HOME=" + systemdQuote(o.DataDir) + "\nRestart=on-failure\nRestartSec=5s\nTimeoutStopSec=30s\nUMask=0077\n\n[Install]\nWantedBy=multi-user.target\n"
	path := filepath.Join(unitDir, serviceName(mode)+".service")
	if err := os.WriteFile(path, []byte(unit), 0600); err != nil {
		return err
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("systemd system manager unavailable: %w", err)
	}
	if err := exec.Command("systemctl", "enable", "--now", serviceName(mode)).Run(); err != nil {
		return err
	}
	return nil
}

func Service(mode, action string, o Options) error {
	if mode != "loadbalancer" && mode != "ingress" {
		return errors.New("service management is available for loadbalancer and ingress only")
	}
	if action == "" {
		return errors.New("service action is required")
	}
	if action == "install" {
		var err error
		o, err = defaults(mode, o)
		if err != nil {
			return err
		}
		return installService(mode, o)
	}
	if action == "uninstall" {
		_ = exec.Command("systemctl", "disable", "--now", serviceName(mode)).Run()
		return os.Remove(filepath.Join("/etc/systemd/system", serviceName(mode)+".service"))
	}
	args := []string{action, serviceName(mode)}
	if action == "logs" {
		args = []string{"status", serviceName(mode), "--no-pager"}
	}
	return exec.Command("systemctl", args...).Run()
}

func Doctor(mode string, o Options) error {
	var err error
	o, err = defaults(mode, o)
	if err != nil {
		return err
	}
	if mode == "loadbalancer" {
		if err := loadbalancerPreflight(); err != nil {
			return err
		}
	} else if err := ingressPreflight(); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(o.StateDir, "state.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("%s prerequisites are ready\n", mode)
	return nil
}

func systemdQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
