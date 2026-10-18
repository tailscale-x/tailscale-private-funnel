package deploymentcli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tailscale-x/tailscale-private-funnel/internal/app"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/native"
	"github.com/tailscale-x/tailscale-private-funnel/internal/provision"
	"github.com/tailscale-x/tailscale-private-funnel/internal/secretbox"
	"google.golang.org/protobuf/proto"
)

type transferPackage struct {
	Version    int                        `json:"version"`
	Deployment *v1.DeploymentInstructions `json:"deployment"`
	Control    *app.ControlTransfer       `json:"control"`
}

func TransferRemote(version string) *cobra.Command {
	f := Flags{}
	var source, out, sourceContext, sourceNamespace, sourceKubeconfig string
	c := &cobra.Command{Use: "transfer-remote NAME <native|k8s>", Short: "Stop and remove a control after saving its complete destination installer", Args: cobra.ExactArgs(2)}
	common(c, &f, version)
	creation(c, &f, "controls", true)
	c.Flags().StringVar(&source, "source", "", "source deployment: native or k8s (required)")
	c.Flags().StringVar(&out, "out", "", "new private installer file, outside source storage (required)")
	c.Flags().BoolVar(&f.Yes, "yes", false, "confirm source shutdown and removal after verified export")
	c.Flags().StringVar(&sourceContext, "source-context", "", "explicit source Kubernetes context")
	c.Flags().StringVar(&sourceNamespace, "source-namespace", "", "explicit source Kubernetes namespace")
	c.Flags().StringVar(&sourceKubeconfig, "source-kubeconfig", "", "source kubeconfig file")
	c.Flags().StringVar(&f.Context, "context", "", "destination Kubernetes context; used only by the installer")
	c.Flags().StringVar(&f.Namespace, "namespace", "", "destination Kubernetes namespace; required for k8s")
	c.Flags().StringVar(&f.StateDir, "state-dir", "", "destination native identity path")
	c.Flags().StringVar(&f.DataDir, "data-dir", "", "destination native data path")
	c.Flags().StringVar(&f.DatabaseURLFile, "database-url-file", "", "protected destination PostgreSQL connection file")
	c.Flags().StringVar(&f.DatabaseCAFile, "database-ca-file", "", "destination PostgreSQL CA file")
	_ = c.Flags().MarkHidden("control-url")
	_ = c.Flags().MarkHidden("administrator")
	c.RunE = func(c *cobra.Command, args []string) error {
		if source != "native" && source != "k8s" {
			return errors.New("--source native or k8s is required")
		}
		if args[1] != "native" && args[1] != "k8s" {
			return errors.New("destination must be native or k8s")
		}
		if out == "" {
			return errors.New("--out is required")
		}
		if !f.DryRun && !f.Yes {
			return errors.New("transfer removes source state; --yes is required")
		}
		if !provision.ValidVersion(f.Version) {
			return errors.New("--version requires a pinned version containing transfer support")
		}
		if err := native.ValidName(args[0]); err != nil {
			return err
		}
		if f.ControlURL != "" || len(f.Administrators) > 0 {
			return errors.New("transfer preserves control URL identity and access settings; change access through the restored control")
		}
		if args[1] == "native" {
			if c.Flags().Changed("storage-size") || c.Flags().Changed("storage-class") || c.Flags().Changed("node-selector") {
				return errors.New("PVC and Pod placement settings require k8s destination")
			}
			f.StorageSize = ""
			f.StorageClass = ""
		}
		if args[1] == "native" && (f.Context != "" || f.Namespace != "" || f.PlacementFile != "") {
			return errors.New("Kubernetes destination settings require k8s destination")
		}
		if source == "native" && (sourceContext != "" || sourceNamespace != "" || sourceKubeconfig != "") {
			return errors.New("source Kubernetes settings require --source k8s")
		}
		if args[1] == "k8s" && (f.Context == "" || f.Namespace == "") {
			return errors.New("destination requires explicit --context and --namespace")
		}
		if source == "k8s" && (sourceContext == "" || sourceNamespace == "") {
			return errors.New("source requires explicit --source-context and --source-namespace")
		}
		if source == "native" && !f.DryRun {
			if handled, err := elevate(c, f); handled {
				return err
			}
		}
		ctx, cancel := context.WithTimeout(c.Context(), f.Timeout)
		defer cancel()
		var src transferSource
		if source == "native" {
			m := native.Default()
			r, err := m.Get(ctx, "controls", args[0])
			if err != nil {
				return err
			}
			if r.Version != f.Version {
				return errors.New("upgrade and start the native source control at the selected transfer version first")
			}
			if err = m.ValidateTransfer(r); err != nil {
				return err
			}
			src = &nativeTransferSource{manager: m, resource: r}
		} else {
			s, err := newKubernetesTransferSource(ctx, args[0], Flags{Context: sourceContext, Namespace: sourceNamespace, Kubeconfig: sourceKubeconfig, Version: f.Version})
			if err != nil {
				return err
			}
			src = s
		}
		defer src.Close()
		hostname, tailnet := src.Settings()
		if f.Hostname != "" && f.Hostname != hostname {
			return errors.New("transfer must preserve the source hostname")
		}
		if f.Tailnet != "" && f.Tailnet != tailnet {
			return errors.New("transfer must preserve the source tailnet")
		}
		f.Hostname, f.Tailnet = hostname, tailnet
		target := "native"
		if args[1] == "k8s" {
			target = "kubernetes"
		}
		req, err := request("controls", args[0], f, target)
		if err != nil {
			return err
		}
		if f.PlacementFile != "" {
			if err = bundlePlacement(req, f); err != nil {
				return err
			}
		}
		p, err := provision.Resolve(req, "", f.Version)
		if err != nil {
			return err
		}
		file, err := validateTransferOutput(out, src.Paths())
		if err != nil {
			return err
		}
		if f.DryRun {
			return output(c, map[string]any{"dry_run": true, "source": source, "destination": args[1], "installer_file": file, "deployment": p.Deployment}, f.Output)
		}
		digest, err := executeTransfer(ctx, src, p, file, c.ErrOrStderr())
		if err != nil {
			return err
		}
		return output(c, map[string]any{"installer_file": file, "sha256": digest, "source_removed": true, "execute": "sh " + provision.Quote(file), "sensitive": true}, f.Output)
	}
	return c
}

type transferSource interface {
	Settings() (string, string)
	Paths() []string
	Fence(context.Context) error
	Export(context.Context) (*app.ControlTransfer, error)
	Remove(context.Context, string, string) error
	Close()
}
type nativeTransferSource struct {
	manager  *native.Manager
	resource native.Resource
}

func (s *nativeTransferSource) Close() {}
func (s *nativeTransferSource) Settings() (string, string) {
	return s.resource.Hostname, s.resource.Tailnet
}
func (s *nativeTransferSource) Paths() []string {
	return []string{s.resource.DataDir, s.resource.StateDir}
}
func (s *nativeTransferSource) Fence(ctx context.Context) error {
	return s.manager.FenceTransfer(ctx, &s.resource)
}
func (s *nativeTransferSource) Export(ctx context.Context) (*app.ControlTransfer, error) {
	r := s.resource
	return app.ExportStoppedControl(ctx, app.Options{StateDir: r.StateDir, DataDir: r.DataDir, Hostname: r.Hostname, Tailnet: r.Tailnet, Database: r.Database}, nil)
}
func (s *nativeTransferSource) Remove(ctx context.Context, id, digest string) error {
	return s.manager.RemoveTransferred(ctx, s.resource, id)
}

func validateTransferOutput(path string, owned []string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err = os.Lstat(abs); err == nil || !os.IsNotExist(err) {
		return "", errors.New("--out must be a new file; existing files and symlinks are refused")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	for _, root := range owned {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		if abs == resolved || strings.HasPrefix(abs, resolved+string(os.PathSeparator)) {
			return "", errors.New("--out must be outside source-owned directories")
		}
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", errors.New("--out parent directory must exist")
	}
	return abs, nil
}
func writeTransferInstaller(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".funnel-transfer-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	defer temp.Close()
	if err = temp.Chmod(0600); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	check, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, check) {
		return errors.New("installer verification failed")
	}
	// link is atomic and, unlike Rename, never overwrites an existing destination.
	if err = os.Link(name, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	check, err = os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, check) {
		return errors.New("saved installer verification failed")
	}
	return nil
}

func TransferInstaller(target string) *cobra.Command {
	c := &cobra.Command{Use: "restore", Hidden: true, Args: cobra.NoArgs}
	c.RunE = func(c *cobra.Command, _ []string) error {
		if target == "native" {
			if handled, err := elevate(c, Flags{}); handled {
				return err
			}
		}
		p, err := readTransferPackage(c.InOrStdin(), target)
		if err != nil {
			return err
		}
		if err = rotateTransferCredentials(p); err != nil {
			return err
		}
		d := p.Deployment.Deployment
		if target == "native" {
			m := native.Default()
			m.Binary, err = os.Executable()
			if err != nil {
				return err
			}
			r, err := m.Resolve("controls", d.ResourceName, "control", d.DisplayName, d.Hostname, p.Deployment.Version, d.StateDir, d.DataDir)
			if err != nil {
				return err
			}
			r.Tailnet = d.Tailnet
			m.Bootstrap = func(ctx context.Context, r *native.Resource, _ *v1.DeploymentInstructions, _ []string) error {
				return app.RestoreControl(ctx, app.Options{StateDir: r.StateDir, DataDir: r.DataDir, Database: r.Database}, p.Control)
			}
			ctx, cancel := context.WithTimeout(c.Context(), time.Duration(d.TimeoutSeconds)*time.Second)
			defer cancel()
			_, err = m.Create(ctx, r, p.Deployment, nil)
			if err != nil {
				return err
			}
			if d.Wait {
				_, err = m.Wait(ctx, "controls", d.ResourceName)
			}
			return err
		}
		return installKubernetesTransfer(c.Context(), p)
	}
	return c
}
func readTransferPackage(r io.Reader, target string) (*transferPackage, error) {
	raw, err := io.ReadAll(io.LimitReader(r, (512<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 512<<20 {
		return nil, errors.New("transfer exceeds 512 MiB limit")
	}
	var p transferPackage
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil {
		return nil, errors.New("invalid transfer package")
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("unexpected transfer data")
	}
	if p.Version != 1 || p.Deployment == nil || p.Deployment.Deployment == nil || p.Deployment.DryRun {
		return nil, errors.New("invalid transfer version or deployment")
	}
	if err = p.Control.Validate(); err != nil {
		return nil, err
	}
	d := p.Deployment.Deployment
	if d.Target == "kubernetes" && (d.Context == "" || d.Namespace == "") {
		return nil, errors.New("transfer requires explicit destination context and namespace")
	}
	if d.Mode != "control" || d.Target != target || p.Deployment.Credentials != nil {
		return nil, errors.New("transfer destination mismatch")
	}
	resolved, err := provision.Resolve(d, "", p.Deployment.Version)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(d, resolved.Deployment) || p.Deployment.Image != resolved.Image {
		return nil, errors.New("transfer deployment settings mismatch")
	}
	var settings struct {
		Hostname string `json:"hostname"`
		Tailnet  string `json:"tailnet"`
	}
	if json.Unmarshal(p.Control.Identity["control-settings.json"], &settings) != nil || settings.Hostname != d.Hostname || settings.Tailnet != d.Tailnet {
		return nil, errors.New("transferred identity settings mismatch")
	}
	return &p, nil
}
func rotateTransferCredentials(p *transferPackage) error {
	d := p.Deployment.Deployment
	if d.OauthClientIdFile == "" && d.OauthClientSecretFile == "" {
		return nil
	}
	if d.OauthClientIdFile == "" || d.OauthClientSecretFile == "" {
		return errors.New("OAuth rotation requires both protected credential files")
	}
	replacements := map[string]string{}
	for _, pair := range [][2]string{{"tailscale_oauth_client_id", d.OauthClientIdFile}, {"tailscale_oauth_client_secret", d.OauthClientSecretFile}} {
		b, err := privateFile(pair[1])
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(b)) == "" {
			return errors.New("empty OAuth replacement")
		}
		encrypted, err := secretbox.Seal(p.Control.Identity["master.key"], strings.TrimSpace(string(b)))
		if err != nil {
			return err
		}
		replacements[pair[0]] = encrypted
	}
	for i := range p.Control.Database.Tables {
		t := &p.Control.Database.Tables[i]
		if t.Name != "settings" {
			continue
		}
		names := map[string]int{}
		for j, c := range t.Columns {
			names[c] = j
		}
		if _, ok := names["name"]; !ok {
			return errors.New("invalid transferred settings")
		}
		if _, ok := names["value"]; !ok {
			return errors.New("invalid transferred settings")
		}
		for j := range t.Rows {
			row := t.Rows[j]
			if len(row) != len(t.Columns) {
				return errors.New("invalid transferred settings row")
			}
			if row[names["name"]].Text == nil {
				continue
			}
			name := *row[names["name"]].Text
			if v, ok := replacements[name]; ok {
				row[names["value"]].Text = &v
				delete(replacements, name)
			}
		}
	}
	if len(replacements) > 0 {
		return errors.New("transferred OAuth settings are missing")
	}
	return nil
}

func executeTransfer(ctx context.Context, src transferSource, p *v1.DeploymentInstructions, file string, progress io.Writer) (string, error) {
	fmt.Fprintln(progress, "Stopping and fencing source control. It will remain stopped if transfer fails.")
	if err := src.Fence(ctx); err != nil {
		return "", fmt.Errorf("source fencing failed; inspect source: %w", err)
	}
	b, err := src.Export(ctx)
	if err != nil {
		return "", fmt.Errorf("source remains fenced with data retained; export failed: %w", err)
	}
	if err = b.Validate(); err != nil {
		return "", err
	}
	settings := proto.Clone(p).(*v1.DeploymentInstructions)
	settings.Script = ""
	settings.Commands = nil
	payload, err := json.Marshal(transferPackage{Version: 1, Deployment: settings, Control: b})
	if err != nil {
		return "", err
	}
	if len(payload) > 512<<20 {
		return "", errors.New("transfer exceeds 512 MiB limit; source data retained")
	}
	if _, err = readTransferPackage(bytes.NewReader(payload), p.Deployment.Target); err != nil {
		return "", fmt.Errorf("export validation failed; source data retained: %w", err)
	}
	script := []byte(provision.TransferInstallerScript(p.Version, p.Image, p.Deployment.Target, payload))
	if err = writeTransferInstaller(file, script); err != nil {
		return "", fmt.Errorf("source remains fenced with data retained; installer save failed: %w", err)
	}
	digest := sha256.Sum256(script)
	fmt.Fprintln(progress, "Installer saved and verified. Removing source deployment without revoking its identity or DNS ownership.")
	if err = src.Remove(ctx, b.ID, hex.EncodeToString(digest[:])); err != nil {
		return "", fmt.Errorf("installer saved at %s; source removal incomplete and remains fenced: %w", file, err)
	}
	return hex.EncodeToString(digest[:]), nil
}
