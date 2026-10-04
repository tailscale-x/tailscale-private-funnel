package ingress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExposureLabel is the only Docker label understood by the ingress supervisor.
// Keeping the key in one place lets an installation add aliases without
// accepting the generic caddy label namespace.
const ExposureLabel = "tailscale-private-funnel.expose"

type Spec struct {
	Port     uint16
	Hostname string
	Protocol string
}

var hostnamePattern = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

// ParseLabel parses the consolidated <port>:<hostname>:<protocol> syntax.
// Duplicate exact entries are rejected so an operator gets an actionable
// configuration error instead of silently creating two identities.
func ParseLabel(value string) ([]Spec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	result := make([]Spec, 0, len(parts))
	seen := map[string]struct{}{}
	for index, part := range parts {
		fields := strings.Split(part, ":")
		if len(fields) != 3 {
			return nil, fmt.Errorf("exposure %d must be <container-port>:<public-hostname>:<protocol>", index+1)
		}
		portValue, err := strconv.ParseUint(strings.TrimSpace(fields[0]), 10, 16)
		if err != nil || portValue == 0 {
			return nil, fmt.Errorf("exposure %d has invalid container port", index+1)
		}
		host := strings.ToLower(strings.TrimSpace(fields[1]))
		if !hostnamePattern.MatchString(host) {
			return nil, fmt.Errorf("exposure %d has invalid public hostname %q", index+1, host)
		}
		protocol := strings.ToLower(strings.TrimSpace(fields[2]))
		if protocol != "http" {
			return nil, fmt.Errorf("exposure %d uses unsupported protocol %q; only http is supported", index+1, protocol)
		}
		spec := Spec{Port: uint16(portValue), Hostname: host, Protocol: protocol}
		key := fmt.Sprintf("%d:%s:%s", spec.Port, spec.Hostname, spec.Protocol)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate exposure %q", key)
		}
		seen[key] = struct{}{}
		result = append(result, spec)
	}
	return result, nil
}

// Forward copies a raw TCP stream to target. The supervisor gives each
// forwarding spec its own sidecar, so no HTTP host/path dispatcher is needed.
func Forward(ctx context.Context, listener net.Listener, target string) error {
	if listener == nil || strings.TrimSpace(target) == "" {
		return errors.New("listener and target are required")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	relayCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var active sync.WaitGroup
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()
	defer func() {
		done := make(chan struct{})
		go func() { active.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			cancel()
			<-done
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		active.Add(1)
		go func() { defer active.Done(); relay(relayCtx, conn, target) }()
	}
}

func relay(ctx context.Context, downstream net.Conn, target string) {
	defer downstream.Close()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	upstream, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, downstream); closeWrite(upstream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(downstream, upstream); closeWrite(downstream); done <- struct{}{} }()
	// EOF in one direction is a half-close. Keep the reverse stream alive so
	// uploads and WebSocket close handshakes cannot truncate the response.
	for i := 0; i < 2; i++ {
		select {
		case <-ctx.Done():
			return
		case <-done:
		}
	}
}

func closeWrite(conn net.Conn) {
	if c, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	}
}
func contextErr(ctx context.Context) error { return ctx.Err() }
