package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"connectrpc.com/connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/manage"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
	"tailscale.com/ipn/store/mem"
	"tailscale.com/tsnet"
)

type TunnelEvent struct {
	Phase   string `json:"phase"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message"`
}

// RunTunnel uses host connectivity for phone-home bootstrap, then runs an
// ephemeral embedded identity. Explicit host mode retains borrowed forwarding.
func RunTunnel(ctx context.Context, settings tunnel.Settings, emit func(TunnelEvent)) error {
	return runTunnel(ctx, settings, emit, tsconnection.NewHost)
}

func runTunnel(ctx context.Context, settings tunnel.Settings, emit func(TunnelEvent), newHost func(string, string) *tsconnection.Host) (result error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return errors.New("foreground tunnels support Linux and macOS")
	}
	var err error
	settings.ControlURL, err = manage.ResolveURL(settings.ControlURL)
	if err != nil {
		return err
	}
	settings, err = settings.Resolve()
	if err != nil {
		return err
	}
	dir, err := settings.StateDir()
	if err != nil {
		return err
	}
	lock, err := lockRuntime(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	path := filepath.Join(dir, "tunnel.json")
	encoded, _ := json.Marshal(settings)
	if previous, e := os.ReadFile(path); e == nil {
		var saved tunnel.Settings
		if json.Unmarshal(previous, &saved) != nil || saved != settings {
			return errors.New("saved tunnel settings differ; use a different --name")
		}
	} else if !os.IsNotExist(e) {
		return e
	} else {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(encoded)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if emit == nil {
		emit = func(TunnelEvent) {}
	}
	emit(TunnelEvent{Phase: "connecting", Message: "Checking the connected host Tailscale daemon and loopback backend"})
	temporary, err := os.MkdirTemp("", "private-funnel-tunnel-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	hostDir := dir
	if settings.Connection == "embedded" {
		hostDir = filepath.Join(temporary, "bootstrap-host")
	}
	host := newHost(settings.Socket, hostDir)
	start, cancel := context.WithTimeout(ctx, 10*time.Second)
	status, err := host.Up(start)
	cancel()
	if err != nil {
		return err
	}
	if status.Self == nil {
		return errors.New("host Tailscale identity unavailable")
	}
	var tags []string
	if status.Self.Tags != nil {
		tags = status.Self.Tags.AsSlice()
	}
	if settings.Connection == "host" {
		if err = identity.Authorize(settings.Installation, identity.TailnetGateway, tags); err != nil {
			return fmt.Errorf("host must carry %s; assign tags in Tailscale, then retry: %w", identity.Tag(settings.Installation, identity.TailnetGateway), err)
		}
	}
	probe, cancel := context.WithTimeout(ctx, 3*time.Second)
	conn, err := (&net.Dialer{}).DialContext(probe, "tcp", settings.Target())
	cancel()
	if err != nil {
		return fmt.Errorf("loopback backend %s is unreachable: %w", settings.Target(), err)
	}
	conn.Close()

	var transport tsconnection.Connection = host
	cleanupConnection := host.Close
	if settings.Connection == "embedded" {
		emit(TunnelEvent{Phase: "bootstrapping", Message: "Requesting a single-use ephemeral key through host Tailscale; host identity stays unchanged"})
		bootstrapCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		response, e := funnelv1connect.NewDeploymentServiceClient(host.HTTPClient(), settings.ControlURL).BootstrapTunnel(bootstrapCtx, connect.NewRequest(&v1.TunnelBootstrapRequest{Installation: settings.Installation, Name: settings.Name, Hostname: settings.Hostname, Port: uint32(settings.Port), LocalAddress: settings.LocalAddress}))
		stop()
		if e != nil {
			return fmt.Errorf("phone-home bootstrap: %w", e)
		}
		if response.Msg.AuthKey == "" || response.Msg.Installation != settings.Installation || response.Msg.ExpiresAt <= time.Now().Unix() || response.Msg.ExpiresAt > time.Now().Add(16*time.Minute).Unix() {
			return errors.New("control returned invalid or expired tunnel bootstrap")
		}
		server := &tsnet.Server{Dir: filepath.Join(temporary, "session"), Store: new(mem.Store), Hostname: settings.Name, AuthKey: response.Msg.AuthKey, Ephemeral: true, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
		response.Msg.AuthKey = ""
		joinCtx, done := context.WithTimeout(ctx, 90*time.Second)
		_, e = server.Up(joinCtx)
		server.AuthKey = ""
		done()
		if e != nil {
			_ = server.Close()
			return fmt.Errorf("ephemeral tunnel join: %w", e)
		}
		transport = tsconnection.Wrap(server)
		cleanupConnection = func() error {
			logoutCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
			defer done()
			lc, e := server.LocalClient()
			if e == nil {
				e = lc.Logout(logoutCtx)
			}
			return errors.Join(e, server.Close())
		}
	}
	defer func() {
		if e := cleanupConnection(); e != nil {
			result = errors.Join(result, fmt.Errorf("tunnel connection cleanup: %w", e))
		}
	}()
	o := Options{Runtime: tunnel.Runtime, Hostname: settings.Name, Installation: settings.Installation, TailscaleConnection: settings.Connection, TailscaleSocket: settings.Socket, ManagementURL: settings.ControlURL, StateDir: dir, DataDir: dir}
	c, err := registerRuntime(ctx, "tailnet-gateway", &o, transport)
	if err != nil {
		return err
	}
	listeners := gateway.NewListenerManager(c)
	rpc := funnelv1connect.NewNodeServiceClient(tsnetHTTPClient(c), settings.ControlURL)
	defer func() {
		listeners.Close()
		var localErr error
		if settings.Connection == "host" {
			localErr = host.Close()
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, e := rpc.WithdrawTunnel(cleanup, connect.NewRequest(&v1.Empty{}))
		if e != nil || localErr != nil {
			emit(TunnelEvent{Phase: "cleanup_pending", Message: "Owned cleanup pending until retry or lease expiry: " + errors.Join(e, localErr).Error()})
			result = errors.Join(result, fmt.Errorf("tunnel cleanup pending: %w", errors.Join(e, localErr)))
		} else {
			emit(TunnelEvent{Phase: "stopped", Message: "Tunnel stopped; owned DNS/Service cleanup queued. Host Tailscale identity is unchanged."})
		}
	}()
	agent := report.Agent{NodeID: o.ComponentID, ManagementURL: settings.ControlURL, DataDir: dir, HTTPClient: tsnetHTTPClient(c)}
	lastContact := time.Now()
	lastMessage := ""
	ready := false
	health := &v1.NodeHealth{Online: true}
	reconcile := func() error {
		request, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// Keep liveness independent of provisioning and backend readiness.
		if _, e := rpc.ReportHealth(request, connect.NewRequest(health)); e != nil {
			return e
		}
		lastContact = time.Now()
		response, e := rpc.AdvertiseTunnel(request, connect.NewRequest(&v1.LocalTunnelRequest{Hostname: settings.Hostname, Port: uint32(settings.Port), LocalAddress: settings.LocalAddress}))
		if e != nil {
			return e
		}
		a := response.Msg.Application
		if a == nil || a.Id == "" || a.ServiceName == "" || a.ServicePort == 0 || a.ServicePort > 65535 {
			return errors.New("control returned an incomplete tunnel assignment")
		}
		probe, stop := context.WithTimeout(request, 3*time.Second)
		conn, e := (&net.Dialer{}).DialContext(probe, "tcp", settings.Target())
		stop()
		reachable := e == nil
		if conn != nil {
			conn.Close()
		}
		if reachable {
			e = listeners.Listen(ctx, a.ServiceName, uint16(a.ServicePort), settings.Target())
			if e != nil {
				return fmt.Errorf("Service forwarding unavailable: %w", e)
			}
		} else {
			listeners.Remove(a.ServiceName, uint16(a.ServicePort))
		}
		if e = agent.Send(request, []report.Exposure{{ApplicationID: a.Id, ServiceName: a.ServiceName, PublicHostname: settings.Hostname, BackendHostname: settings.LocalAddress, BackendIP: settings.LocalAddress, Port: settings.Port, Protocol: "http", Reachable: reachable}}); e != nil {
			return e
		}
		if _, e = rpc.Acknowledge(request, connect.NewRequest(&v1.ConfigAck{Revision: nodeconfig.Revision(a), Services: []string{a.ServiceName}})); e != nil {
			return e
		}
		lastContact = time.Now()
		message := response.Msg.Message
		health.Backends = 1
		health.Reachable = 0
		health.Listeners = nil
		health.Errors = nil
		if reachable {
			health.Reachable = 1
			health.Listeners = []string{a.ServiceName}
		} else {
			health.Errors = []string{"Loopback backend is unavailable"}
		}
		if !reachable {
			message = "Local backend is unavailable; the running tunnel retains its hostname registration"
		}
		phase := "waiting"
		if response.Msg.Ready && reachable {
			phase = "ready"
		}
		if message != lastMessage || ready != (phase == "ready") {
			emit(TunnelEvent{Phase: phase, URL: response.Msg.PublicUrl, Message: message})
			lastMessage = message
			ready = phase == "ready"
		}
		return nil
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err = reconcile(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			code := connect.CodeOf(err)
			if code == connect.CodePermissionDenied || code == connect.CodeUnauthenticated || code == connect.CodeInvalidArgument || code == connect.CodeFailedPrecondition || code == connect.CodeAlreadyExists || code == connect.CodeAborted {
				return err
			}
			if time.Since(lastContact) >= 5*time.Minute {
				return fmt.Errorf("control unreachable for five minutes; stopping owned forwarding: %w", err)
			}
			emit(TunnelEvent{Phase: "waiting", Message: err.Error()})
			ready = false
			lastMessage = ""
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
