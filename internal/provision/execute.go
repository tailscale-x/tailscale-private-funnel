package provision

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	container "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

type Command struct {
	Program   string
	Args, Env []string
	Input     []byte
}
type Executor struct {
	KubeContext string
	Run         func(context.Context, Command) ([]byte, error)
	Progress    func(string)
	HTTP        *http.Client
	BinaryDir   string
}

func (e *Executor) run(ctx context.Context, c Command) ([]byte, error) {
	if e.Run != nil {
		return e.Run(ctx, c)
	}
	cmd := exec.CommandContext(ctx, c.Program, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin = bytes.NewReader(c.Input)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", c.Program, err)
	}
	return b, nil
}
func (e *Executor) progress(s string) {
	if e.Progress != nil {
		e.Progress(s)
	}
}
func (e *Executor) http() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}
func artifactURL(version, asset string) string {
	return "https://github.com/tailscale-x/tailscale-private-funnel/releases/download/" + version + "/" + asset
}
func (e *Executor) download(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	r, err := e.http().Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("artifact download: HTTP %d", r.StatusCode)
	}
	_, err = io.Copy(w, io.LimitReader(r.Body, 512<<20))
	return err
}
func (e *Executor) binary(ctx context.Context, p *v1.DeploymentInstructions, dir string) (string, error) {
	path := filepath.Join(dir, "tailscale-private-funnel")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if strings.HasPrefix(p.Version, "dev-") {
		ref, err := name.ParseReference(p.Image)
		if err != nil {
			return "", err
		}
		img, err := remote.Image(ref, remote.WithContext(ctx), remote.WithPlatform(container.Platform{OS: runtime.GOOS, Architecture: runtime.GOARCH}))
		if err != nil {
			return "", err
		}
		layers, err := img.Layers()
		if err != nil {
			return "", err
		}
		found := false
		for _, layer := range layers {
			stream, err := layer.Uncompressed()
			if err != nil {
				return "", err
			}
			reader := tar.NewReader(stream)
			for {
				h, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					stream.Close()
					return "", err
				}
				if strings.TrimPrefix(h.Name, "./") == "usr/local/bin/tailscale-private-funnel" {
					if h.Typeflag != tar.TypeReg || h.Size > 512<<20 {
						stream.Close()
						return "", errors.New("invalid image binary entry")
					}
					if err = f.Truncate(0); err != nil {
						return "", err
					}
					if _, err = f.Seek(0, 0); err != nil {
						return "", err
					}
					if _, err = io.Copy(f, reader); err != nil {
						stream.Close()
						return "", err
					}
					found = true
				}
			}
			stream.Close()
		}
		if !found {
			return "", errors.New("published image contains no Funnel binary")
		}
	} else {
		asset := "tailscale-private-funnel-" + runtime.GOOS + "-" + runtime.GOARCH
		var sums bytes.Buffer
		if err = e.download(ctx, artifactURL(p.Version, "SHA256SUMS"), &sums); err != nil {
			return "", err
		}
		expected := ""
		for _, line := range strings.Split(sums.String(), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 && strings.TrimPrefix(parts[1], "*") == asset {
				expected = parts[0]
			}
		}
		if len(expected) != 64 {
			return "", errors.New("release lacks checksum for this architecture")
		}
		hash := sha256.New()
		if err = e.download(ctx, artifactURL(p.Version, asset), io.MultiWriter(f, hash)); err != nil {
			return "", err
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected {
			return "", errors.New("release checksum mismatch")
		}
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

// DownloadBinary verifies a release checksum or OCI layer digest before returning a standalone executable.
func (e *Executor) DownloadBinary(ctx context.Context, version, image, dir string) (string, error) {
	return e.binary(ctx, &v1.DeploymentInstructions{Version: version, Image: image}, dir)
}
