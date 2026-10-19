package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

// ExposureLabel is the only Docker label understood by the gateway supervisor.
// Keeping the key in one place lets an installation add aliases without
// accepting the generic caddy label namespace.
const ExposureLabel = "tailscale-private-funnel.expose"

type Spec struct {
	PathPrefix string
	Rewrite    *proxy.PrefixRewrite
	Port       uint16
	Hostname   string
	Protocol   string
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

// Forward copies a raw TCP stream to the selected backend address. Each
// Service listener already identifies its one approved backend port.
func Forward(ctx context.Context, listener net.Listener, target string) error {
	d := net.Dialer{Timeout: 5 * time.Second}
	return ForwardDial(ctx, listener, target, d.DialContext)
}
func ForwardDial(ctx context.Context, listener net.Listener, target string, dial func(context.Context, string, string) (net.Conn, error)) error {
	if listener == nil || strings.TrimSpace(target) == "" {
		return errors.New("listener and target are required")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	drainCtx, cancel := context.WithCancel(context.Background())
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
		go func() { defer active.Done(); forwardConn(drainCtx, conn, target, dial) }()
	}
}

func forwardConn(ctx context.Context, downstream net.Conn, target string, dial func(context.Context, string, string) (net.Conn, error)) {
	defer downstream.Close()
	upstream, err := dial(ctx, "tcp", target)
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

// ParseLabels resolves all configured namespaces before touching Docker or control.
func ParseLabels(labels map[string]string, aliases []string) ([]Spec, error) {
	keys := append([]string{ExposureLabel}, aliases...)
	var canonical string
	var out []Spec
	seenKeys := map[string]bool{}
	for _, key := range keys {
		if seenKeys[key] {
			continue
		}
		seenKeys[key] = true
		if value := strings.TrimSpace(labels[key]); value != "" {
			if canonical != "" && canonical != value {
				return nil, fmt.Errorf("exposure aliases conflict")
			}
			canonical = value
		}
	}
	var err error
	out, err = ParseLabel(canonical)
	if err != nil {
		return nil, err
	}
	groups := map[string]map[string]string{}
	for _, key := range keys {
		base := strings.TrimSuffix(key, ".expose") + ".routes."
		for label, value := range labels {
			if !strings.HasPrefix(label, base) {
				continue
			}
			parts := strings.Split(strings.TrimPrefix(label, base), ".")
			if len(parts) != 2 || !routeName.MatchString(parts[0]) {
				return nil, fmt.Errorf("invalid route label %s", label)
			}
			if groups[parts[0]] == nil {
				groups[parts[0]] = map[string]string{}
			}
			field := parts[1]
			switch field {
			case "hostname", "port", "path-prefix", "replace-prefix":
			default:
				return nil, fmt.Errorf("unknown route field %s", field)
			}
			if old, ok := groups[parts[0]][field]; ok && old != value {
				return nil, fmt.Errorf("route aliases conflict for %s", parts[0])
			}
			groups[parts[0]][field] = value
		}
	}
	names := []string{}
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := groups[name]
		parsed, e := ParseLabel(g["port"] + ":" + g["hostname"] + ":http")
		if e != nil || len(parsed) != 1 {
			return nil, fmt.Errorf("invalid route %s: hostname and port required", name)
		}
		spec := parsed[0]
		spec.PathPrefix = g["path-prefix"]
		if e = proxy.ValidatePath(spec.PathPrefix); e != nil {
			return nil, fmt.Errorf("route %s: %w", name, e)
		}
		spec.PathPrefix = NormalizePrefix(spec.PathPrefix)
		if value, ok := g["replace-prefix"]; ok {
			if e = proxy.ValidatePath(value); e != nil {
				return nil, e
			}
			spec.Rewrite = &proxy.PrefixRewrite{ReplacePrefix: value}
		}
		out = append(out, spec)
	}
	claims := map[string]bool{}
	for _, spec := range out {
		key := spec.Hostname + ":" + NormalizePrefix(spec.PathPrefix)
		if claims[key] {
			return nil, fmt.Errorf("duplicate hostname/path claim %s", key)
		}
		claims[key] = true
	}
	return out, nil
}

var routeName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func NormalizePrefix(path string) string {
	p := strings.TrimRight(path, "/")
	if p == "" {
		return "/"
	}
	return p
}
