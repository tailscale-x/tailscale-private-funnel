package ingress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
)

const SidecarIDLabel = "tailscale-private-funnel.sidecar-id"

type Assignment struct {
	ApplicationID string
	ServiceName   string
	ServicePort   uint16
	SidecarID     string
	PublicKey     string
	Enabled       bool
}

type Resolver interface {
	Resolve(ctx context.Context, hostname string, backendPort uint16) (Assignment, error)
}

type StaticResolver map[string]Assignment

func (r StaticResolver) Resolve(_ context.Context, hostname string, backendPort uint16) (Assignment, error) {
	assignment, ok := r[hostname]
	if !ok || !assignment.Enabled || assignment.ServicePort == 0 || assignment.ServicePort != backendPort {
		return Assignment{}, fmt.Errorf("no approved assignment for %s:%d", hostname, backendPort)
	}
	return assignment, nil
}

type Config struct {
	Docker          *dockerclient.Client
	Image           string
	StateRoot       string
	DataRoot        string
	NodeID          string
	ManagementURL   string
	LabelKeys       []string
	DenyNetworks    []string
	Resolver        Resolver
	ListenerManager *ListenerManager
}

type Runtime struct {
	ID              string
	ContainerID     string
	BackendHostname string
	Spec            Spec
	Assignment      Assignment
	BackendIP       string
	Networks        []Network
	Reachable       bool
	RelayPort       int
}

// Supervisor owns the Docker API. Its containers only receive bind mounts and
// selected networks; the Docker socket is intentionally absent from every
// sidecar HostConfig.
func (c Config) Reconcile(ctx context.Context) ([]Runtime, error) {
	if c.Docker == nil {
		return nil, fmt.Errorf("Docker client is required")
	}
	if c.Image == "" {
		return nil, fmt.Errorf("sidecar image is required")
	}
	if c.Resolver == nil {
		return nil, fmt.Errorf("control assignment resolver is required")
	}
	if c.ListenerManager == nil {
		return nil, fmt.Errorf("ingress listener manager is required")
	}
	containers, err := c.Docker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	active := map[string]struct{}{}
	activeListeners := map[string]struct{}{}
	controlUnavailable := false
	var out []Runtime
	for _, item := range containers {
		value := ""
		for _, key := range append([]string{ExposureLabel}, c.LabelKeys...) {
			if v := strings.TrimSpace(item.Labels[key]); v != "" {
				if value != "" && value != v {
					slog.Warn("ingress exposure aliases conflict", "container", item.ID)
					value = ""
					break
				}
				value = v
			}
		}
		specs, err := ParseLabel(value)
		if err != nil {
			slog.Warn("ingress exposure label rejected", "container", item.ID, "error", err)
			continue
		}
		if len(specs) == 0 {
			continue
		}
		inspect, err := c.Docker.ContainerInspect(ctx, item.ID)
		if err != nil {
			slog.Warn("ingress target inspect failed", "container", item.ID, "error", err)
			continue
		}
		networks, err := c.targetNetworks(ctx, inspect)
		if err != nil {
			slog.Warn("ingress target network inspect failed", "container", item.ID, "error", err)
			continue
		}
		if len(networks) == 0 {
			continue
		}
		for _, spec := range specs {
			assignment, err := c.Resolver.Resolve(ctx, spec.Hostname, spec.Port)
			if err != nil {
				slog.Warn("ingress assignment resolution failed", "container", item.ID, "hostname", spec.Hostname, "port", spec.Port, "error", err)
				if errors.Is(err, ErrControlUnavailable) {
					controlUnavailable = true
				}
				continue
			}
			// The sidecar identity is tied to the approved Service and backend
			// port, rather than the ephemeral Docker container ID. A replacement
			// application container therefore reuses the same tsnet identity.
			sidecarID := StableSidecarID(assignment.ServiceName, spec)
			active[sidecarID] = struct{}{}
			activeListeners[fmt.Sprintf("%s:%d", assignment.ServiceName, assignment.ServicePort)] = struct{}{}
			backendIP := networks[0].IPAddress
			if backendIP == "" {
				continue
			}
			backendHostname := "container"
			if len(item.Names) > 0 {
				backendHostname = strings.TrimPrefix(item.Names[0], "/")
			}
			runtime := Runtime{ID: sidecarID, ContainerID: item.ID, BackendHostname: backendHostname, Spec: spec, Assignment: assignment, BackendIP: backendIP, Networks: networks}
			runtime.Assignment.SidecarID = sidecarID
			relayPort, ensureErr := c.ensureRelay(ctx, runtime, item.ID)
			if ensureErr != nil {
				slog.Warn("ingress relay unavailable", "sidecar", runtime.ID, "container", item.ID, "error", ensureErr)
				runtime.Reachable = false
			} else {
				runtime.RelayPort = relayPort
				listenErr := c.ListenerManager.Listen(ctx, runtime.Assignment.ServiceName, runtime.Assignment.ServicePort, net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)))
				runtime.Reachable = listenErr == nil
				if listenErr != nil {
					slog.Warn("ingress Service listener unavailable", "service", runtime.Assignment.ServiceName, "error", listenErr)
				}
			}
			out = append(out, runtime)
		}
	}
	// Any managed sidecar not represented by the current label snapshot is
	// drained and removed. This only touches containers carrying our sidecar ID.
	if controlUnavailable {
		// A transient control outage must not drain already-approved listeners.
		// They retain their persistent identities and will be reconciled after
		// the signed control configuration becomes available again.
		return out, ErrControlUnavailable
	}
	for _, item := range containers {
		id := strings.TrimSpace(item.Labels[SidecarIDLabel])
		if id == "" {
			continue
		}
		if _, ok := active[id]; !ok {
			_ = c.Docker.ContainerStop(ctx, item.ID, container.StopOptions{Timeout: intPtr(10)})
			_ = c.Docker.ContainerRemove(ctx, item.ID, container.RemoveOptions{Force: true})
		}
	}
	// Drain Service listeners whose approved assignment disappeared.
	c.ListenerManager.Reconcile(activeListeners)
	return out, nil
}

func (c Config) targetNetworks(ctx context.Context, inspect types.ContainerJSON) ([]Network, error) {
	var raw []Network
	for name, endpoint := range inspect.NetworkSettings.Networks {
		info, err := c.Docker.NetworkInspect(ctx, endpoint.NetworkID, network.InspectOptions{})
		if err != nil {
			return nil, err
		}
		cidr := ""
		if len(info.IPAM.Config) > 0 {
			cidr = info.IPAM.Config[0].Subnet
		}
		raw = append(raw, Network{ID: endpoint.NetworkID, Name: name, IPAddress: endpoint.IPAddress, CIDR: cidr, UserDefined: name != "bridge" && name != "host" && name != "none" && info.Name != "bridge"})
	}
	selected := SelectNetworks(raw, c.DenyNetworks)
	if err := ValidateNoOverlap(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

func (c Config) ensureRelay(ctx context.Context, runtime Runtime, targetID string) (int, error) {
	name := "tailscale-private-funnel-sidecar-" + runtime.ID
	if existing, err := c.Docker.ContainerInspect(ctx, name); err == nil {
		labels := map[string]string{}
		if existing.Config != nil {
			labels = existing.Config.Labels
		}
		matches := labels["tailscale-private-funnel.target"] == targetID &&
			labels["tailscale-private-funnel.backend-ip"] == runtime.BackendIP &&
			labels["tailscale-private-funnel.backend-port"] == strconv.Itoa(int(runtime.Spec.Port)) &&
			labels["tailscale-private-funnel.service-name"] == runtime.Assignment.ServiceName &&
			labels["tailscale-private-funnel.service-port"] == strconv.Itoa(int(runtime.Assignment.ServicePort))
		if !matches {
			if existing.State != nil && existing.State.Running {
				_ = c.Docker.ContainerStop(ctx, existing.ID, container.StopOptions{Timeout: intPtr(10)})
			}
			if removeErr := c.Docker.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true}); removeErr != nil {
				return 0, removeErr
			}
		} else {
			relayPort, parseErr := strconv.Atoi(labels["tailscale-private-funnel.relay-port"])
			if parseErr != nil || relayPort == 0 {
				return 0, fmt.Errorf("relay port label missing")
			}
			if existing.State == nil || !existing.State.Running {
				if startErr := c.Docker.ContainerStart(ctx, existing.ID, container.StartOptions{}); startErr != nil {
					return 0, startErr
				}
			}
			if connectErr := c.connectMissingNetworks(ctx, existing, runtime.Networks); connectErr != nil {
				return 0, connectErr
			}
			return relayPort, nil
		}
	} else if !errdefs.IsNotFound(err) {
		return 0, err
	}
	relayPort, err := freeRelayPort()
	if err != nil {
		return 0, err
	}
	port := nat.Port(strconv.Itoa(relayPort) + "/tcp")
	cmd := []string{"ingress", "relay", "serve", "--listen", ":" + strconv.Itoa(relayPort), "--backend-ip", runtime.BackendIP, "--backend-port", strconv.Itoa(int(runtime.Spec.Port))}
	config := &container.Config{
		Image: c.Image, User: "65532", Cmd: cmd,
		ExposedPorts: nat.PortSet{port: struct{}{}},
		Labels: map[string]string{
			SidecarIDLabel: runtime.ID, "tailscale-private-funnel.target": targetID,
			"tailscale-private-funnel.backend-ip":   runtime.BackendIP,
			"tailscale-private-funnel.backend-port": strconv.Itoa(int(runtime.Spec.Port)),
			"tailscale-private-funnel.service-name": runtime.Assignment.ServiceName,
			"tailscale-private-funnel.service-port": strconv.Itoa(int(runtime.Assignment.ServicePort)),
			"tailscale-private-funnel.relay-port":   strconv.Itoa(relayPort),
		},
	}
	hostConfig := &container.HostConfig{PortBindings: nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: strconv.Itoa(relayPort)}}}}
	endpoints := map[string]*network.EndpointSettings{}
	endpoints[runtime.Networks[0].Name] = &network.EndpointSettings{}
	created, err := c.Docker.ContainerCreate(ctx, config, hostConfig, &network.NetworkingConfig{EndpointsConfig: endpoints}, nil, name)
	if err != nil {
		return 0, err
	}
	for _, selected := range runtime.Networks[1:] {
		if err := c.Docker.NetworkConnect(ctx, selected.ID, created.ID, &network.EndpointSettings{}); err != nil {
			_ = c.Docker.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
			return 0, err
		}
	}
	if err := c.Docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return 0, err
	}
	return relayPort, nil
}

func (c Config) connectMissingNetworks(ctx context.Context, existing types.ContainerJSON, selected []Network) error {
	attached := map[string]bool{}
	selectedIDs := map[string]bool{}
	for _, selectedNetwork := range selected {
		selectedIDs[selectedNetwork.ID] = true
	}
	if existing.NetworkSettings != nil {
		for networkID, endpoint := range existing.NetworkSettings.Networks {
			if endpoint != nil {
				attached[networkID] = true
				if !selectedIDs[networkID] {
					if err := c.Docker.NetworkDisconnect(ctx, networkID, existing.ID, true); err != nil && !errdefs.IsNotFound(err) {
						return err
					}
				}
			}
		}
	}
	for _, selectedNetwork := range selected {
		if attached[selectedNetwork.ID] {
			continue
		}
		if err := c.Docker.NetworkConnect(ctx, selectedNetwork.ID, existing.ID, &network.EndpointSettings{}); err != nil {
			return err
		}
	}
	return nil
}

func StableSidecarID(serviceName string, spec Spec) string {
	key := serviceName
	if strings.TrimSpace(key) == "" {
		key = spec.Hostname
	}
	// Multiple aliases for one approved Service and backend port share the
	// same sidecar. Different ports or Services produce separate identities.
	sum := sha256.Sum256([]byte(key + "\x00" + strconv.Itoa(int(spec.Port)) + "\x00" + spec.Protocol))
	return hex.EncodeToString(sum[:8])
}
func intPtr(v int) *int { return &v }

func freeRelayPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
