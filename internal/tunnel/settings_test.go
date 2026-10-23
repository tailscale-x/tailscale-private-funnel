package tunnel

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsAndSecretFreeCommand(t *testing.T) {
	s := Settings{Hostname: "App.Example.com.", Port: 8080, ControlURL: "http://control.example.ts.net:8080"}
	a, err := s.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Resolve()
	if a.Connection != "embedded" {
		t.Fatal("phone-home is not the default", a.Connection)
	}
	if a.Name != b.Name || a.Hostname != "app.example.com" || a.LocalAddress != "127.0.0.1" {
		t.Fatal(a, b)
	}
	cmd, err := Command(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tailscale-private-funnel tunnel 8080", "--hostname 'app.example.com'", "--control-url 'http://control.example.ts.net:8080'"} {
		if !strings.Contains(cmd, want) {
			t.Fatal(cmd)
		}
	}
	for _, bad := range []string{"sudo", "systemd", "launchctl", "docker", "auth-key", "oauth", "--tailscale 'embedded'"} {
		if strings.Contains(cmd, bad) {
			t.Fatal(cmd)
		}
	}
	s.LocalAddress = "::1"
	a, err = s.Resolve()
	if err != nil || a.Target() != "[::1]:8080" {
		t.Fatal(a, err)
	}
	if a.Name == b.Name {
		t.Fatal("different backend addresses share local state")
	}
	path, err := a.StateDir()
	if err != nil || !filepath.IsAbs(path) || !strings.Contains(path, filepath.Join("tailscale-private-funnel", "tunnels", "main")) {
		t.Fatal(path, err)
	}
}

func TestInvalidTunnelSettings(t *testing.T) {
	for _, address := range []string{"0.0.0.0", "::", "192.168.1.2", "100.64.0.1", "localhost", "::1%lo0", "127.0.0.1:8080"} {
		t.Run(address, func(t *testing.T) {
			if _, err := (Settings{Hostname: "app.example.com", Port: 8080, LocalAddress: address}).Resolve(); err == nil {
				t.Fatal("nonliteral or nonloopback target accepted")
			}
		})
	}
	for _, s := range []Settings{{Connection: "invalid", Hostname: "app.example.com", Port: 8080}, {Hostname: "*.example.com", Port: 8080}, {Hostname: "app.example.com"}, {Hostname: "app.example.com", Port: 8080, Name: "../escape"}, {Hostname: "app.example.com", Port: 8080, Installation: "../installation"}} {
		if _, err := s.Resolve(); err == nil {
			t.Fatal(s)
		}
	}
	if _, err := Command(Settings{Hostname: "app.example.com", Port: 8080, ControlURL: "http://secret@example.ts.net"}); err == nil {
		t.Fatal("credential-bearing control URL accepted")
	}
}

func TestExplicitHostCommandKeepsRoleTaggedOptIn(t *testing.T) {
	cmd, err := Command(Settings{Connection: "host", Hostname: "app.example.com", Port: 8080, ControlURL: "http://control.example.ts.net:8080"})
	if err != nil || !strings.Contains(cmd, "--tailscale host") {
		t.Fatal(cmd, err)
	}
}
