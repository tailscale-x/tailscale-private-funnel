// Package native owns named Linux services. It never manages remote hosts.
package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"k8s.io/apimachinery/pkg/util/validation"
)

type Resource struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	DisplayName string          `json:"display_name"`
	Kind        string          `json:"kind"`
	Mode        string          `json:"mode"`
	Hostname    string          `json:"hostname"`
	Version     string          `json:"version"`
	StateDir    string          `json:"state_dir"`
	DataDir     string          `json:"data_dir"`
	Unit        string          `json:"unit"`
	LastReport  int64           `json:"last_report,omitempty"`
	NodeID      string          `json:"node_id,omitempty"`
	ControlURL  string          `json:"control_url,omitempty"`
	SetupURL    string          `json:"setup_url,omitempty"`
	Tailnet     string          `json:"tailnet,omitempty"`
	Phase       string          `json:"phase"`
	Diagnostics []string        `json:"diagnostics,omitempty"`
	Runtime     json.RawMessage `json:"runtime,omitempty"`
}
type Manager struct {
	ConfigDir, StateRoot, BinaryRoot, UnitDir string
	Binary                                    string
	Run                                       func(context.Context, string, []string, io.Reader, []string) ([]byte, error)
	Cleanup                                   func(context.Context, Resource) error
	Bootstrap                                 func(context.Context, *Resource, *v1.DeploymentInstructions, []string) error
}

func Default() *Manager {
	return &Manager{ConfigDir: "/etc/tailscale-private-funnel/native", StateRoot: "/var/lib/tailscale-private-funnel/native", BinaryRoot: "/usr/local/lib/tailscale-private-funnel", UnitDir: "/etc/systemd/system"}
}
func ValidName(s string) error {
	if len(s) > 53 || len(validation.IsDNS1123Label(s)) != 0 {
		return errors.New("name must be a DNS label of at most 53 characters")
	}
	return nil
}
func (m *Manager) path(kind, name string) string {
	return filepath.Join(m.ConfigDir, kind, name+".json")
}
func (m *Manager) run(ctx context.Context, program string, args []string, input io.Reader, env []string) ([]byte, error) {
	if m.Run != nil {
		return m.Run(ctx, program, args, input, env)
	}
	c := exec.CommandContext(ctx, program, args...)
	c.Stdin = input
	c.Env = append(os.Environ(), env...)
	return c.CombinedOutput()
}
func (m *Manager) save(r *Resource) error {
	p := m.path(r.Kind, r.Name)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	tmp := p + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, p)
}
func (m *Manager) Get(ctx context.Context, kind, name string) (Resource, error) {
	var r Resource
	if e := ValidName(name); e != nil {
		return r, e
	}
	b, e := os.ReadFile(m.path(kind, name))
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	// Registry snapshots never establish liveness for a new process.
	r.Runtime = nil
	r.LastReport = 0
	r.SetupURL = ""
	if out, e := m.run(ctx, "systemctl", []string{"is-active", r.Unit}, nil, nil); e == nil && strings.TrimSpace(string(out)) == "active" {
		r.Phase = "Running"
		mainPID, pidErr := m.run(ctx, "systemctl", []string{"show", r.Unit, "--property=MainPID", "--value"}, nil, nil)
		runtimePID, _ := os.ReadFile(filepath.Join(r.DataDir, "serve.pid"))
		if b, e := os.ReadFile(filepath.Join(r.DataDir, "runtime-status.json")); e == nil && pidErr == nil && strings.TrimSpace(string(mainPID)) != "0" && strings.TrimSpace(string(mainPID)) == strings.TrimSpace(string(runtimePID)) {
			r.Runtime = b
		}
	} else if r.Phase != "Cleanup pending" && r.Phase != "Failed" {
		r.Phase = "Stopped"
	}
	if b, e := os.ReadFile(filepath.Join(r.DataDir, "last-control-report")); e == nil {
		r.LastReport, _ = strconv.ParseInt(string(b), 10, 64)
	}
	if r.Mode != "control" && len(r.Runtime) > 0 {
		var status map[string]any
		if json.Unmarshal(r.Runtime, &status) == nil {
			reportInfo, reportErr := os.Stat(filepath.Join(r.DataDir, "last-control-report"))
			processInfo, processErr := os.Stat(filepath.Join(r.DataDir, "serve.pid"))
			fresh := reportErr == nil && processErr == nil && !reportInfo.ModTime().Before(processInfo.ModTime()) && r.LastReport > time.Now().Add(-90*time.Second).Unix()
			proxyReady := r.Mode != "loadbalancer" || status["ready"] == true
			status["ready"] = r.Phase == "Running" && status["state"] == "Running" && fresh && proxyReady
			r.Runtime, _ = json.Marshal(status)
		}
	}
	if r.Mode == "control" && r.Phase == "Running" {
		var status struct {
			Ready bool     `json:"ready"`
			IPs   []string `json:"ips"`
		}
		if json.Unmarshal(r.Runtime, &status) == nil && status.Ready && len(status.IPs) > 0 {
			r.SetupURL = "http://" + net.JoinHostPort(status.IPs[0], "8080")
		}
	} else {
		r.SetupURL = ""
	}
	return r, nil
}
func (m *Manager) List(ctx context.Context, kind string) ([]Resource, error) {
	entries, e := os.ReadDir(filepath.Join(m.ConfigDir, kind))
	if os.IsNotExist(e) {
		return []Resource{}, nil
	}
	if e != nil {
		return nil, e
	}
	out := []Resource{}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			r, e := m.Get(ctx, kind, strings.TrimSuffix(entry.Name(), ".json"))
			if e != nil {
				return nil, e
			}
			out = append(out, r)
		}
	}
	return out, nil
}
func (m *Manager) Resolve(kind, name, mode, display, hostname, version, state, data string) (Resource, error) {
	r := Resource{Name: name, DisplayName: display, Kind: kind, Mode: mode, Hostname: hostname, Version: version, Phase: "Pending"}
	if e := ValidName(name); e != nil {
		return r, e
	}
	if kind != "controls" && kind != "gateways" && kind != "loadbalancers" {
		return r, errors.New("invalid native resource")
	}
	switch kind {
	case "controls":
		if mode != "control" {
			return r, errors.New("invalid control mode")
		}
	case "loadbalancers":
		if mode != "loadbalancer" {
			return r, errors.New("invalid load-balancer mode")
		}
	case "gateways":
		if mode != "docker-gateway" && mode != "tailnet-gateway" {
			return r, errors.New("native gateways require --type docker or tailnet")
		}
	}
	if r.Hostname == "" {
		r.Hostname = name
	}
	if e := ValidName(r.Hostname); e != nil {
		return r, e
	}
	if r.DisplayName == "" {
		r.DisplayName = name
	}
	if _, e := os.Stat(m.path(kind, name)); e == nil {
		return r, errors.New("resource already exists; use update")
	}
	id, e := store.NewResourceID()
	if e != nil {
		return r, e
	}
	r.ID = id
	r.Unit = "tailscale-private-funnel-" + id + ".service"
	base := filepath.Join(m.StateRoot, kind, id)
	r.StateDir = state
	r.DataDir = data
	if state == "" {
		r.StateDir = filepath.Join(base, "state")
	}
	if data == "" {
		r.DataDir = filepath.Join(base, "data")
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		if !filepath.IsAbs(p) || filepath.Clean(p) == "/" {
			return r, errors.New("safe absolute state/data paths required")
		}
		if files, e := os.ReadDir(p); e == nil && len(files) > 0 {
			return r, errors.New("state/data directory is not empty")
		}
	}
	if r.StateDir == r.DataDir || strings.HasPrefix(r.StateDir, r.DataDir+"/") || strings.HasPrefix(r.DataDir, r.StateDir+"/") {
		return r, errors.New("state/data paths must not overlap")
	}
	return r, nil
}
func (m *Manager) Create(ctx context.Context, r Resource, p *v1.DeploymentInstructions, oauth []string) (Resource, error) {
	if os.Geteuid() != 0 && m.Run == nil {
		return r, errors.New("native mutations require root or sudo")
	}
	if _, e := m.run(ctx, "systemctl", []string{"--version"}, nil, nil); e != nil {
		return r, errors.New("systemd is required")
	}
	if e := os.MkdirAll(filepath.Dir(m.path(r.Kind, r.Name)), 0700); e != nil {
		return r, e
	}
	claim, e := os.OpenFile(m.path(r.Kind, r.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return r, fmt.Errorf("resource already exists or cannot be reserved: %w", e)
	}
	if e = json.NewEncoder(claim).Encode(r); e != nil {
		claim.Close()
		return r, e
	}
	claim.Close()
	if m.StateRoot == "/var/lib/tailscale-private-funnel/native" {
		packageRoot := filepath.Dir(m.StateRoot)
		if e := os.MkdirAll(packageRoot, 0711); e != nil {
			return r, e
		}
		if e := os.Chmod(packageRoot, 0711); e != nil {
			return r, e
		}
	}
	// Package-owned ancestors contain no credentials; permit traversal to private per-resource directories.
	for _, dir := range []string{m.StateRoot, filepath.Join(m.StateRoot, r.Kind), filepath.Join(m.StateRoot, r.Kind, r.ID)} {
		if e := os.MkdirAll(dir, 0755); e != nil {
			return r, e
		}
		if e := os.Chmod(dir, 0711); e != nil {
			return r, e
		}
	}
	for _, path := range []string{r.StateDir, r.DataDir} {
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			return r, e
		}
		parent := filepath.Clean(filepath.Dir(path))
		if strings.HasPrefix(parent, filepath.Clean(m.StateRoot)+string(os.PathSeparator)) {
			if e := os.Chmod(parent, 0711); e != nil {
				return r, e
			}
		}
		if entries, e := os.ReadDir(path); e == nil && len(entries) > 0 {
			return r, errors.New("state/data directory is not empty")
		}
		if e := os.MkdirAll(path, 0700); e != nil {
			return r, e
		}
		if e := os.WriteFile(filepath.Join(path, ".funnel-native-resource"), []byte(r.ID), 0600); e != nil {
			return r, e
		}
	}
	if p != nil && p.Credentials != nil {
		r.NodeID = p.Credentials.EnrollmentId
		r.ControlURL = p.ManagementUrl
	}
	if e := m.save(&r); e != nil {
		return r, e
	}
	fail := func(e error) (Resource, error) {
		r.Phase = "Failed"
		r.Diagnostics = []string{e.Error()}
		_ = m.save(&r)
		return r, e
	}
	if m.Bootstrap != nil {
		if e := m.Bootstrap(ctx, &r, p, oauth); e != nil {
			return fail(e)
		}
	} else {
		args := []string{"native", "runtime", r.Mode, "serve", "--bootstrap-only", "--hostname", r.Hostname, "--state-dir", r.StateDir, "--data-dir", r.DataDir}
		var input io.Reader
		env := []string{}
		if r.Mode == "control" {
			args = append(args, "--tailnet", r.Tailnet)
			args = append(args, oauth...)
		} else {
			if p == nil || p.Credentials == nil || p.Credentials.ExpiresAt < time.Now().Unix() {
				return fail(errors.New("fresh bootstrap credentials required"))
			}
			args = append(args, "--bootstrap-stdin")
			b, _ := json.Marshal(p)
			input = strings.NewReader(string(b))
		}
		if _, e := m.run(ctx, m.Binary, args, input, env); e != nil {
			return fail(fmt.Errorf("bootstrap failed: %w", e))
		}
	}
	if e := m.install(ctx, &r); e != nil {
		return fail(e)
	}
	r.Phase = "Starting"
	_ = m.save(&r)
	return r, nil
}
func (m *Manager) install(ctx context.Context, r *Resource) error {
	if e := m.prepareRuntimeAccount(ctx, r); e != nil {
		return e
	}
	source := m.Binary
	if source == "" {
		var e error
		source, e = os.Executable()
		if e != nil {
			return e
		}
	}
	dest := filepath.Join(m.BinaryRoot, r.Version, "tailscale-private-funnel")
	if source != dest {
		if _, e := m.run(ctx, "install", []string{"-D", "-o", "root", "-g", "root", "-m", "0555", source, dest}, nil, nil); e != nil {
			return e
		}
	}
	args := []string{dest, "native", "runtime", r.Mode, "serve", "--hostname", r.Hostname, "--state-dir", r.StateDir, "--data-dir", r.DataDir}
	if r.Mode == "control" {
		args = append(args, "--tailnet", r.Tailnet)
	} else {
		args = append(args, "--config", filepath.Join(r.DataDir, "config.json"))
	}
	quoted := make([]string, len(args))
	for i, v := range args {
		quoted[i] = strconv.Quote(strings.ReplaceAll(v, "%", "%%"))
	}
	privilege := "User=tailscale-private-funnel\nGroup=tailscale-private-funnel\nNoNewPrivileges=true\nProtectSystem=strict\nProtectHome=read-only\nPrivateTmp=true\nProtectKernelTunables=true\nProtectKernelModules=true\nProtectControlGroups=true\nRestrictSUIDSGID=true\nReadWritePaths=" + strconv.Quote(r.StateDir) + " " + strconv.Quote(r.DataDir) + "\n"
	if r.Mode == "loadbalancer" {
		privilege += "CapabilityBoundingSet=CAP_NET_BIND_SERVICE\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n"
	} else {
		privilege += "CapabilityBoundingSet=\n"
	}
	if r.Mode == "docker-gateway" {
		privilege += "SupplementaryGroups=docker\n"
	}
	unit := "[Unit]\nDescription=Tailscale Private Funnel " + r.Kind + " " + r.Name + "\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nExecStart=" + strings.Join(quoted, " ") + "\n" + privilege + "Environment=FUNNEL_DEPLOYMENT_TARGET=native\nEnvironment=FUNNEL_RESOURCE_NAME=" + r.Name + "\nEnvironment=" + strconv.Quote("HOME="+r.DataDir) + "\nEnvironment=" + strconv.Quote("XDG_DATA_HOME="+r.DataDir) + "\nEnvironment=" + strconv.Quote("XDG_CONFIG_HOME="+r.DataDir+"/config") + "\nRestart=on-failure\nRestartSec=5\nTimeoutStopSec=60\nUMask=0077\n\n[Install]\nWantedBy=multi-user.target\n"
	if e := os.WriteFile(filepath.Join(m.UnitDir, r.Unit), []byte(unit), 0644); e != nil {
		return e
	}
	if _, e := m.run(ctx, "systemctl", []string{"daemon-reload"}, nil, nil); e != nil {
		return e
	}
	_, e := m.run(ctx, "systemctl", []string{"enable", "--now", r.Unit}, nil, nil)
	return e
}
func (m *Manager) Service(ctx context.Context, kind, name, action string) (Resource, error) {
	r, e := m.Get(ctx, kind, name)
	if e != nil {
		return r, e
	}
	if r.Phase == "Cleanup pending" && action != "stop" {
		return r, errors.New("deletion is pending; retry delete instead of starting the node")
	}
	if action != "start" && action != "stop" && action != "restart" {
		return r, errors.New("invalid service action")
	}
	_, e = m.run(ctx, "systemctl", []string{action, r.Unit}, nil, nil)
	return r, e
}
func (m *Manager) Logs(ctx context.Context, kind, name string) ([]byte, error) {
	r, e := m.Get(ctx, kind, name)
	if e != nil {
		return nil, e
	}
	return m.run(ctx, "journalctl", []string{"-u", r.Unit, "--no-pager", "-n", "100"}, nil, nil)
}
func (m *Manager) Update(ctx context.Context, kind, name, version, display string) (Resource, error) {
	r, e := m.Get(ctx, kind, name)
	if e != nil {
		return r, e
	}
	if r.Phase == "Cleanup pending" {
		return r, errors.New("deletion is pending; retry delete")
	}
	if _, e = m.run(ctx, "systemctl", []string{"stop", r.Unit}, nil, nil); e != nil {
		return r, e
	}
	if version != "" {
		r.Version = version
	}
	if display != "" {
		r.DisplayName = display
	}
	if e = m.install(ctx, &r); e != nil {
		return r, e
	}
	r.Phase = "Starting"
	return r, m.save(&r)
}
func (m *Manager) Delete(ctx context.Context, kind, name string) error {
	r, e := m.Get(ctx, kind, name)
	if e != nil {
		return e
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		b, err := os.ReadFile(filepath.Join(p, ".funnel-native-resource"))
		if err != nil || string(b) != r.ID {
			return errors.New("state ownership marker mismatch")
		}
	}
	if r.Mode == "control" {
		if e = app.ControlDeletionReady(app.Options{StateDir: r.StateDir, DataDir: r.DataDir, Hostname: r.Hostname}); e != nil {
			return e
		}
	}
	unitPresent := true
	if _, e = os.Stat(filepath.Join(m.UnitDir, r.Unit)); os.IsNotExist(e) {
		load, _ := m.run(ctx, "systemctl", []string{"show", r.Unit, "--property=LoadState", "--value"}, nil, nil)
		if strings.TrimSpace(string(load)) != "not-found" {
			return errors.New("owned unit file missing but service state is not verified absent")
		}
		unitPresent = false
	} else if e != nil {
		return e
	}
	if unitPresent {
		if _, e = m.run(ctx, "systemctl", []string{"stop", r.Unit}, nil, nil); e != nil {
			return e
		}
		if _, e = m.run(ctx, "systemctl", []string{"disable", r.Unit}, nil, nil); e != nil {
			return e
		}
	}
	r.Phase = "Cleanup pending"
	_ = m.save(&r)
	if _, e = os.Stat(filepath.Join(r.StateDir, "joined")); e == nil {
		if m.Cleanup != nil {
			e = m.Cleanup(ctx, r)
		} else {
			e = m.cleanupRuntime(ctx, &r)
		}
		if e != nil {
			return fmt.Errorf("cleanup failed; identity and data retained: %w", e)
		}
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		b, e := os.ReadFile(filepath.Join(p, ".funnel-native-resource"))
		if e != nil || string(b) != r.ID {
			return errors.New("state ownership marker mismatch")
		}
	}
	if unitPresent {
		if _, e = m.run(ctx, "systemctl", []string{"disable", r.Unit}, nil, nil); e != nil {
			return e
		}
	}
	if e = os.Remove(filepath.Join(m.UnitDir, r.Unit)); e != nil && !os.IsNotExist(e) {
		return e
	}
	for _, p := range []string{r.StateDir, r.DataDir} {
		if e = os.RemoveAll(p); e != nil {
			return e
		}
	}
	if e = os.Remove(m.path(kind, name)); e != nil {
		return e
	}
	_, e = m.run(ctx, "systemctl", []string{"daemon-reload"}, nil, nil)
	return e
}
func (m *Manager) Wait(ctx context.Context, kind, name string) (Resource, error) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		r, e := m.Get(ctx, kind, name)
		if e != nil {
			return r, e
		}
		var status struct {
			Ready bool `json:"ready"`
		}
		_ = json.Unmarshal(r.Runtime, &status)
		if r.Phase == "Running" && len(r.Runtime) > 0 && status.Ready {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return r, ctx.Err()
		case <-tick.C:
		}
	}
}

// ResolveDeployment normalizes API input without retaining bootstrap credentials in the registry.
func ResolveDeployment(in *v1.DeploymentRequest, url, version string) (*v1.DeploymentInstructions, error) {
	return provision.Resolve(in, url, version)
}

// Lifecycle cleanup uses the same Unix identity as normal serving. This keeps
// tsnet's host-network routing behavior and state ownership consistent.
func (m *Manager) prepareRuntimeAccount(ctx context.Context, r *Resource) error {
	for _, dir := range []string{m.StateRoot, filepath.Join(m.StateRoot, r.Kind), filepath.Dir(r.StateDir), filepath.Dir(r.DataDir)} {
		clean := filepath.Clean(dir)
		if clean == filepath.Clean(m.StateRoot) || strings.HasPrefix(clean, filepath.Clean(m.StateRoot)+string(os.PathSeparator)) {
			if e := os.Chmod(clean, 0711); e != nil {
				return e
			}
		}
	}
	if _, err := m.run(ctx, "id", []string{"-u", "tailscale-private-funnel"}, nil, nil); err != nil {
		if _, err = m.run(ctx, "useradd", []string{"--system", "--user-group", "--home-dir", "/nonexistent", "--shell", "/usr/sbin/nologin", "tailscale-private-funnel"}, nil, nil); err != nil {
			return fmt.Errorf("create runtime account: %w", err)
		}
	}
	for _, dir := range []string{r.StateDir, r.DataDir} {
		if _, err := m.run(ctx, "chown", []string{"-R", "tailscale-private-funnel:tailscale-private-funnel", dir}, nil, nil); err != nil {
			return err
		}
	}

	for _, dir := range []string{r.StateDir, r.DataDir} {
		if _, err := m.run(ctx, "runuser", []string{"--user", "tailscale-private-funnel", "--", "test", "-x", dir}, nil, nil); err != nil {
			return fmt.Errorf("runtime account cannot traverse %s; choose accessible system state/data paths", dir)
		}
	}
	return nil
}
func (m *Manager) cleanupRuntime(ctx context.Context, r *Resource) error {
	if e := m.prepareRuntimeAccount(ctx, r); e != nil {
		return e
	}
	source := m.Binary
	if source == "" {
		var e error
		source, e = os.Executable()
		if e != nil {
			return e
		}
	}
	if e := os.MkdirAll(m.BinaryRoot, 0755); e != nil {
		return e
	}
	if e := os.Chmod(m.BinaryRoot, 0755); e != nil {
		return e
	}
	scratch, e := os.MkdirTemp(m.BinaryRoot, ".cleanup-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(scratch)
	if e = os.Chmod(scratch, 0755); e != nil {
		return e
	}
	binary := filepath.Join(scratch, "tailscale-private-funnel")
	if _, e = m.run(ctx, "install", []string{"-o", "root", "-g", "root", "-m", "0555", source, binary}, nil, nil); e != nil {
		return e
	}
	args := []string{"--user", "tailscale-private-funnel", "--", binary, "native", "runtime", r.Mode, "serve", "--cleanup-only", "--hostname", r.Hostname, "--state-dir", r.StateDir, "--data-dir", r.DataDir}
	if r.Mode != "control" {
		args = append(args, "--config", filepath.Join(r.DataDir, "config.json"))
	}
	output, e := m.run(ctx, "runuser", args, nil, nil)
	if e != nil {
		return fmt.Errorf("runtime cleanup: %w: %s", e, strings.TrimSpace(string(output)))
	}
	return nil
}
