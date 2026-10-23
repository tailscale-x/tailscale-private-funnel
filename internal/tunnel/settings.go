// Package tunnel defines foreground local tunnels and phone-home bootstrap settings.
package tunnel

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
)

const Runtime = "foreground-tunnel"

type Settings struct {
	Connection   string `json:"connection"`
	Hostname     string `json:"hostname"`
	Port         uint16 `json:"port"`
	LocalAddress string `json:"local_address"`
	Name         string `json:"name"`
	Installation string `json:"installation"`
	ControlURL   string `json:"control_url"`
	Socket       string `json:"socket,omitempty"`
}

func (s Settings) Resolve() (Settings, error) {
	if s.Connection == "" {
		s.Connection = "embedded"
	}
	if s.Connection != "embedded" && s.Connection != "host" {
		return s, fmt.Errorf("--tailscale must be embedded or host")
	}
	s.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s.Hostname), "."))
	if err := policy.ValidateHostname(s.Hostname); err != nil {
		return s, fmt.Errorf("valid --hostname required: %w", err)
	}
	if s.Port == 0 {
		return s, fmt.Errorf("local port must be between 1 and 65535")
	}
	if s.LocalAddress == "" {
		s.LocalAddress = "127.0.0.1"
	}
	ip, err := netip.ParseAddr(s.LocalAddress)
	if err != nil || !ip.IsLoopback() || ip.Zone() != "" {
		return s, fmt.Errorf("--local-address must be a loopback IP such as 127.0.0.1 or ::1")
	}
	s.LocalAddress = ip.String()
	if s.Installation == "" {
		s.Installation = "main"
	}
	if err = identity.ValidateInstallation(s.Installation); err != nil {
		return s, err
	}
	if s.Socket != "" && !filepath.IsAbs(s.Socket) {
		return s, fmt.Errorf("--tailscale-socket must be an absolute LocalAPI socket path")
	}
	if s.Name == "" {
		s.Name = fmt.Sprintf("tunnel-%x", sha256.Sum256([]byte(s.ControlURL+"/"+s.Installation+"/"+s.Hostname+"/"+s.Target()+"/"+s.Connection)))[:23]
	}
	if !regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,51}[a-z0-9])?$`).MatchString(s.Name) {
		return s, fmt.Errorf("--name must be a DNS label of at most 53 characters")
	}
	return s, nil
}
func (s Settings) Target() string { return net.JoinHostPort(s.LocalAddress, fmt.Sprint(s.Port)) }

// StateDir contains component metadata only, never a Tailscale identity or key.
func (s Settings) StateDir() (string, error) {
	var base string
	if runtime.GOOS == "darwin" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	} else {
		base = os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "state")
		}
	}
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("state directory must be absolute")
	}
	return filepath.Join(base, "tailscale-private-funnel", "tunnels", s.Installation, s.Name), nil
}

func Command(s Settings) (string, error) {
	u, e := url.Parse(s.ControlURL)
	if e != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("a private control HTTP(S) origin without credentials is required")
	}
	s, err := s.Resolve()
	if err != nil {
		return "", err
	}
	quote := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
	cmd := "tailscale-private-funnel tunnel " + fmt.Sprint(s.Port) + " --hostname " + quote(s.Hostname) + " --control-url " + quote(s.ControlURL) + " --installation " + quote(s.Installation)
	if s.Name != "" {
		cmd += " --name " + quote(s.Name)
	}
	if s.LocalAddress != "127.0.0.1" {
		cmd += " --local-address " + quote(s.LocalAddress)
	}
	if s.Connection == "host" {
		cmd += " --tailscale host"
	}
	if s.Socket != "" {
		cmd += " --tailscale-socket " + quote(s.Socket)
	}
	return cmd, nil
}
