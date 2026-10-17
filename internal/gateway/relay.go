package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const relayPort = "10000/tcp"

func (c Config) ensureRelay(ctx context.Context, r Runtime) (string, error) {
	if c.RelayImage == "" || c.NodeID == "" {
		return "", fmt.Errorf("isolated Docker relay requires image and node identity")
	}
	target := net.JoinHostPort(r.Networks[0].IPAddress, strconv.Itoa(int(r.Spec.Port)))
	name := c.relayName(r)
	info, err := c.Docker.ContainerInspect(ctx, name)
	if err == nil {
		if info.Config == nil || info.Config.Labels["tailscale-private-funnel.relay-owner"] != c.NodeID || info.Config.Labels["tailscale-private-funnel.target"] != r.ContainerID {
			return "", fmt.Errorf("relay name belongs to an unmanaged container")
		}
		if info.Config.Image != c.RelayImage {
			if e := c.Docker.ContainerRemove(ctx, info.ID, container.RemoveOptions{Force: true}); e != nil {
				return "", e
			}
			return c.ensureRelay(ctx, r)
		}
	}
	if err != nil {
		if !dockerclient.IsErrNotFound(err) {
			return "", fmt.Errorf("inspect relay: %w", err)
		}
		if e := c.ensureRelayImage(ctx); e != nil {
			return "", e
		}
		config := &container.Config{Image: c.RelayImage, Cmd: []string{"native", "runtime", "docker-gateway", "relay", "serve", "--listen=:10000", "--target=" + target}, ExposedPorts: nat.PortSet{nat.Port(relayPort): {}}, User: "65532:65532", Labels: map[string]string{"tailscale-private-funnel.relay-owner": c.NodeID, "tailscale-private-funnel.target": r.ContainerID}}
		host := &container.HostConfig{ReadonlyRootfs: true, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, PortBindings: nat.PortMap{nat.Port(relayPort): {{HostIP: "127.0.0.1", HostPort: "0"}}}, NetworkMode: container.NetworkMode(r.Networks[0].ID)}
		created, e := c.Docker.ContainerCreate(ctx, config, host, &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{r.Networks[0].Name: {NetworkID: r.Networks[0].ID}}}, nil, name)
		if e != nil {
			return "", e
		}
		for _, n := range r.Networks[1:] {
			if e = c.Docker.NetworkConnect(ctx, n.ID, created.ID, &network.EndpointSettings{}); e != nil {
				_ = c.Docker.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
				return "", e
			}
		}
		if e = c.Docker.ContainerStart(ctx, created.ID, container.StartOptions{}); e != nil {
			return "", e
		}
		info, err = c.Docker.ContainerInspect(ctx, created.ID)
	}
	if err == nil && info.State != nil && !info.State.Running {
		err = c.Docker.ContainerStart(ctx, info.ID, container.StartOptions{})
		if err == nil {
			info, err = c.Docker.ContainerInspect(ctx, info.ID)
		}
	}
	if err != nil || info.NetworkSettings == nil {
		return "", fmt.Errorf("relay inspection failed: %v", err)
	}
	ports := info.NetworkSettings.Ports[nat.Port(relayPort)]
	if len(ports) != 1 || ports[0].HostIP != "127.0.0.1" {
		return "", fmt.Errorf("relay must publish loopback only")
	}
	address := net.JoinHostPort("127.0.0.1", ports[0].HostPort)
	for i := 0; i < 10; i++ {
		conn, e := net.DialTimeout("tcp", address, 200*time.Millisecond)
		if e == nil {
			conn.Close()
			if err := c.checkRelayBackend(ctx, info.ID, target); err != nil {
				return "", err
			}
			return address, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("relay/backend is unreachable")
}

// Native installations need the relay image even when no image has previously
// been pulled on this Docker host. Reuse the pinned local image when available.
func (c Config) ensureRelayImage(ctx context.Context) error {
	if _, _, err := c.Docker.ImageInspectWithRaw(ctx, c.RelayImage); err == nil {
		return nil
	} else if !dockerclient.IsErrNotFound(err) {
		return fmt.Errorf("inspect relay image: %w", err)
	}
	stream, err := c.Docker.ImagePull(ctx, c.RelayImage, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull relay image %s: %w", c.RelayImage, err)
	}
	defer stream.Close()
	decoder := json.NewDecoder(stream)
	for {
		var progress struct {
			Error string `json:"error"`
		}
		if err := decoder.Decode(&progress); err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("read relay image pull: %w", err)
		}
		if progress.Error != "" {
			return fmt.Errorf("pull relay image %s: %s", c.RelayImage, progress.Error)
		}
	}
}
func (c Config) cleanupRelays(ctx context.Context, targets map[string]bool) {
	if c.NodeID == "" {
		return
	}
	items, err := c.Docker.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", "tailscale-private-funnel.relay-owner="+c.NodeID))})
	if err != nil {
		return
	}
	for _, item := range items {
		active := false
		for _, name := range item.Names {
			active = active || targets[strings.TrimPrefix(name, "/")]
		}
		if !active {
			_ = c.Docker.ContainerRemove(ctx, item.ID, container.RemoveOptions{Force: true})
		}
	}
}

// ServeRelay has no enrollment, tsnet state, Docker client, or HTTP layer.
func ServeRelay(ctx context.Context, listen, target string) error {
	if _, _, err := net.SplitHostPort(target); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		return fmt.Errorf("backend reachability: %w", err)
	}
	conn.Close()
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() { <-ctx.Done(); ln.Close() }()
	return ForwardDial(ctx, ln, target, (&net.Dialer{}).DialContext)
}
func (c Config) Cleanup(ctx context.Context) { c.cleanupRelays(ctx, map[string]bool{}) }

func (c Config) relayName(r Runtime) string {
	ids := []string{}
	for _, n := range r.Networks {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)
	target := net.JoinHostPort(r.Networks[0].IPAddress, strconv.Itoa(int(r.Spec.Port)))
	return "tpf-relay-" + fmt.Sprintf("%x", sha256.Sum256([]byte(c.NodeID+r.ContainerID+target+fmt.Sprint(ids))))[:20]
}
func (c Config) checkRelayBackend(ctx context.Context, id, target string) error {
	exec, err := c.Docker.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: RelayCheckCommand(target), User: "65532:65532"})
	if err != nil {
		return err
	}
	if err = c.Docker.ContainerExecStart(ctx, exec.ID, container.ExecStartOptions{Detach: true}); err != nil {
		return err
	}
	for i := 0; i < 40; i++ {
		state, e := c.Docker.ContainerExecInspect(ctx, exec.ID)
		if e != nil {
			return e
		}
		if !state.Running {
			if state.ExitCode != 0 {
				return fmt.Errorf("backend TCP probe inside relay failed")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("relay probe timed out")
}
func CheckRelay(target string) error {
	conn, err := net.DialTimeout("tcp", target, 3*time.Second)
	if conn != nil {
		conn.Close()
	}
	return err
}

// RelayCheckCommand is shared with the command-tree contract test.
func RelayCheckCommand(target string) []string {
	return []string{"/usr/local/bin/tailscale-private-funnel", "native", "runtime", "docker-gateway", "relay", "check", "--target=" + target}
}
