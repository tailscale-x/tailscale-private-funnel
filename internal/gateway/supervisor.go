package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
)

type Assignment struct {
	ApplicationID string
	ServiceName   string
	ServicePort   uint16
	PublicKey     string
	Enabled       bool
}

type Resolver interface {
	Resolve(ctx context.Context, spec Spec) (Assignment, error)
}

type StaticResolver map[string]Assignment

func (r StaticResolver) Resolve(_ context.Context, spec Spec) (Assignment, error) {
	hostname, backendPort := spec.Hostname, spec.Port
	assignment, ok := r[hostname]
	if !ok || !assignment.Enabled || assignment.ServicePort == 0 || assignment.ServicePort != backendPort {
		return Assignment{}, fmt.Errorf("no approved assignment for %s:%d", hostname, backendPort)
	}
	return assignment, nil
}

type Config struct {
	RelayImage           string
	NodeID               string
	Docker               *dockerclient.Client
	LabelKeys            []string
	DenyNetworks         []string
	Resolver             Resolver
	ListenerManager      *ListenerManager
	DeferListenerCleanup bool
}

type Runtime struct {
	BackendDeviceID string
	ContainerID     string
	BackendHostname string
	Spec            Spec
	Assignment      Assignment
	BackendIP       string
	Networks        []Network
	Reachable       bool
}

// Supervisor owns Docker discovery and forwards approved Services directly to
// their selected application containers on user-defined bridge networks.
func (c Config) Reconcile(ctx context.Context) ([]Runtime, error) {
	if c.Docker == nil {
		return nil, fmt.Errorf("Docker client is required")
	}
	if c.Resolver == nil {
		return nil, fmt.Errorf("control assignment resolver is required")
	}
	if c.ListenerManager == nil {
		return nil, fmt.Errorf("gateway listener manager is required")
	}
	containers, err := c.Docker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	claims := map[string]int{}
	for _, item := range containers {
		if item.State != "running" {
			continue
		}
		specs, e := ParseLabels(item.Labels, c.LabelKeys)
		if e != nil {
			continue
		}
		for _, spec := range specs {
			claims[spec.Hostname+":"+NormalizePrefix(spec.PathPrefix)]++
		}
	}
	activeListeners := map[string]struct{}{}
	controlUnavailable := false
	targets := map[string]bool{}
	var out []Runtime
	for _, item := range containers {
		if item.State != "running" {
			continue
		}
		specs, err := ParseLabels(item.Labels, c.LabelKeys)
		if err != nil {
			slog.Warn("gateway exposure label rejected", "container", item.ID, "error", err)
			continue
		}
		if len(specs) == 0 {
			continue
		}
		inspect, err := c.Docker.ContainerInspect(ctx, item.ID)
		if err != nil {
			slog.Warn("gateway target inspect failed", "container", item.ID, "error", err)
			continue
		}
		networks, err := c.targetNetworks(ctx, inspect)
		if err != nil {
			slog.Warn("gateway target network inspect failed", "container", item.ID, "error", err)
			continue
		}
		if len(networks) == 0 {
			continue
		}
		for _, spec := range specs {
			if claims[spec.Hostname+":"+NormalizePrefix(spec.PathPrefix)] > 1 {
				slog.Warn("duplicate Docker route claim rejected", "host", spec.Hostname, "path", spec.PathPrefix)
				continue
			}
			assignment, err := c.Resolver.Resolve(ctx, spec)
			if err != nil {
				slog.Warn("gateway assignment resolution failed", "container", item.ID, "hostname", spec.Hostname, "port", spec.Port, "error", err)
				if errors.Is(err, ErrControlUnavailable) {
					controlUnavailable = true
				}
				continue
			}
			backendHostname := "container"
			if len(item.Names) > 0 {
				backendHostname = strings.TrimPrefix(item.Names[0], "/")
			}
			runtime := Runtime{ContainerID: item.ID, BackendHostname: backendHostname, Spec: spec, Assignment: assignment, Networks: networks}
			backendIP := networks[0].IPAddress
			backendAddress, probeErr := c.ensureRelay(ctx, runtime)
			if probeErr != nil {
				slog.Warn("gateway backend unavailable", "container", item.ID, "port", spec.Port, "error", probeErr)
				runtime.Reachable = false
			} else {
				runtime.BackendIP = backendIP
				listenErr := c.ListenerManager.Listen(ctx, runtime.Assignment.ServiceName, runtime.Assignment.ServicePort, backendAddress)
				runtime.Reachable = listenErr == nil
				if listenErr != nil {
					slog.Warn("gateway Service listener unavailable", "service", runtime.Assignment.ServiceName, "error", listenErr)
				}
			}
			if runtime.Reachable {
				targets[c.relayName(runtime)] = true
				activeListeners[fmt.Sprintf("%s:%d", assignment.ServiceName, assignment.ServicePort)] = struct{}{}
			}
			out = append(out, runtime)
		}
	}
	if controlUnavailable {
		// A transient control outage must not drain already-approved listeners.
		// They retain their persistent identities and will be reconciled after
		// the signed control configuration becomes available again.
		return out, ErrControlUnavailable
	}
	// Drain Service listeners whose approved assignment disappeared.
	c.cleanupRelays(ctx, targets)
	if !c.DeferListenerCleanup {
		c.ListenerManager.Reconcile(activeListeners)
	}
	return out, nil
}

func (c Config) targetNetworks(ctx context.Context, inspect types.ContainerJSON) ([]Network, error) {
	if inspect.NetworkSettings == nil {
		return nil, fmt.Errorf("target container has no network settings")
	}
	var raw []Network
	for name, endpoint := range inspect.NetworkSettings.Networks {
		if endpoint == nil {
			continue
		}
		info, err := c.Docker.NetworkInspect(ctx, endpoint.NetworkID, network.InspectOptions{})
		if err != nil {
			return nil, err
		}
		cidr := ""
		if len(info.IPAM.Config) > 0 {
			cidr = info.IPAM.Config[0].Subnet
		}
		ip := endpoint.IPAddress
		if ip == "" {
			ip = endpoint.GlobalIPv6Address
		}
		raw = append(raw, Network{ID: endpoint.NetworkID, Name: name, IPAddress: ip, CIDR: cidr, UserDefined: name != "bridge" && name != "host" && name != "none" && info.Name != "bridge"})
	}
	selected := SelectNetworks(raw, c.DenyNetworks)
	if err := ValidateNoOverlap(selected); err != nil {
		return nil, err
	}
	return selected, nil
}

// backendEndpoint selects an address only from locally allowed application
// networks and confirms that the Docker host can reach the requested port.
func backendEndpoint(ctx context.Context, selected []Network, port uint16) (string, string, error) {
	if port == 0 {
		return "", "", fmt.Errorf("backend port is required")
	}
	var lastErr error
	for _, network := range selected {
		address := strings.TrimSpace(network.IPAddress)
		if net.ParseIP(address) == nil {
			lastErr = fmt.Errorf("network %s has no usable container IP", network.Name)
			continue
		}
		target := net.JoinHostPort(address, strconv.Itoa(int(port)))
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		conn, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", target)
		cancel()
		if err == nil {
			_ = conn.Close()
			return address, target, nil
		}
		lastErr = fmt.Errorf("network %s backend %s: %w", network.Name, target, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no selected user-defined Docker network")
	}
	return "", "", fmt.Errorf("backend is unreachable from the gateway host: %w", lastErr)
}
