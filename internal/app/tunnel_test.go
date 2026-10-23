package app

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/dnstype"
	"tailscale.com/types/views"
)

type tunnelRPCFixture struct {
	funnelv1connect.UnimplementedNodeServiceHandler
	registered, health, reported, acked, withdrawn int
	port                                           uint32
}

func (f *tunnelRPCFixture) RegisterComponent(_ context.Context, r *connect.Request[v1.RegisterComponentRequest]) (*connect.Response[v1.RegisteredComponent], error) {
	if r.Msg.Deployment.Runtime != tunnel.Runtime || r.Msg.Deployment.TailscaleConnection != "host" {
		return nil, errors.New("not a foreground host component")
	}
	f.registered++
	return connect.NewResponse(&v1.RegisteredComponent{}), nil
}
func (f *tunnelRPCFixture) ReportHealth(context.Context, *connect.Request[v1.NodeHealth]) (*connect.Response[v1.Empty], error) {
	f.health++
	return connect.NewResponse(&v1.Empty{}), nil
}
func (f *tunnelRPCFixture) AdvertiseTunnel(_ context.Context, r *connect.Request[v1.LocalTunnelRequest]) (*connect.Response[v1.LocalTunnelStatus], error) {
	if r.Msg.Port != f.port || r.Msg.Hostname != "app.example.com" {
		return nil, errors.New("incorrect assignment")
	}
	return connect.NewResponse(&v1.LocalTunnelStatus{Application: &v1.RuntimeApplication{Id: "local-app", ServiceName: "svc:local-app", ServicePort: f.port}, Ready: true, PublicUrl: "https://app.example.com", Message: "Ready"}), nil
}
func (f *tunnelRPCFixture) SubmitReport(_ context.Context, r *connect.Request[v1.ExposureReport]) (*connect.Response[v1.Empty], error) {
	if len(r.Msg.Exposures) != 1 || !r.Msg.Exposures[0].Reachable {
		return nil, errors.New("reachable loopback report required")
	}
	f.reported++
	return connect.NewResponse(&v1.Empty{}), nil
}
func (f *tunnelRPCFixture) Acknowledge(context.Context, *connect.Request[v1.ConfigAck]) (*connect.Response[v1.Empty], error) {
	f.acked++
	return connect.NewResponse(&v1.Empty{}), nil
}
func (f *tunnelRPCFixture) WithdrawTunnel(context.Context, *connect.Request[v1.Empty]) (*connect.Response[v1.TestResult], error) {
	f.withdrawn++
	return connect.NewResponse(&v1.TestResult{Ok: true}), nil
}

type tunnelHostFixture struct {
	mu      sync.Mutex
	cfg     *ipn.ServeConfig
	prefs   ipn.Prefs
	control string
}

func (f *tunnelHostFixture) Status(context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{BackendState: "Running", Self: &ipnstate.PeerStatus{ID: tailcfg.StableNodeID("borrowed-device"), Tags: func() *views.Slice[string] {
		v := views.SliceOf([]string{identity.Tag("main", identity.TailnetGateway)})
		return &v
	}(), TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}}}, nil
}
func (f *tunnelHostFixture) GetServeConfig(context.Context) (*ipn.ServeConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg, nil
}
func (f *tunnelHostFixture) SetServeConfig(_ context.Context, c *ipn.ServeConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg = c
	return nil
}
func (f *tunnelHostFixture) GetPrefs(context.Context) (*ipn.Prefs, error) { return &f.prefs, nil }
func (f *tunnelHostFixture) EditPrefs(_ context.Context, p *ipn.MaskedPrefs) (*ipn.Prefs, error) {
	if !p.AdvertiseServicesSet {
		return nil, errors.New("unexpected daemon mutation")
	}
	f.prefs.AdvertiseServices = p.AdvertiseServices
	return &f.prefs, nil
}
func (f *tunnelHostFixture) QueryDNS(context.Context, string, string) ([]byte, []*dnstype.Resolver, error) {
	return nil, nil, errors.New("not needed")
}
func (f *tunnelHostFixture) UserDial(ctx context.Context, network, _ string, _ uint16) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, f.control)
}

func TestForegroundTunnelReportsReadinessAndWithdrawsOnCancel(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			c, e := backend.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	_, portText, _ := net.SplitHostPort(backend.Addr().String())
	port, _ := strconv.Atoi(portText)
	fixture := &tunnelRPCFixture{port: uint32(port)}
	_, handler := funnelv1connect.NewNodeServiceHandler(fixture)
	server := httptest.NewServer(handler)
	defer server.Close()
	api := &tunnelHostFixture{control: server.Listener.Addr().String(), cfg: &ipn.ServeConfig{TCP: map[uint16]*ipn.TCPPortHandler{19001: {TCPForward: "127.0.0.1:19002"}}}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	var phases []string
	err = runTunnel(ctx, tunnel.Settings{Connection: "host", Hostname: "app.example.com", Port: uint16(port), ControlURL: "http://100.64.0.12:8080"}, func(event TunnelEvent) {
		phases = append(phases, event.Phase)
		if event.Phase == "ready" {
			cancel()
		}
	}, func(_ string, dir string) *tsconnection.Host {
		return &tsconnection.Host{API: api, DataDir: dir, LockPath: filepath.Join(t.TempDir(), "host.lock")}
	})
	if err != nil {
		t.Fatal(err)
	}
	if fixture.registered != 1 || fixture.health != 1 || fixture.reported != 1 || fixture.acked != 1 || fixture.withdrawn != 1 {
		t.Fatal("missing lifecycle call", fixture)
	}
	if len(phases) != 3 || phases[0] != "connecting" || phases[1] != "ready" || phases[2] != "stopped" {
		t.Fatal(phases)
	}
	if len(api.cfg.Services) != 0 || api.cfg.TCP[19001].TCPForward != "127.0.0.1:19002" || len(api.prefs.AdvertiseServices) != 0 {
		t.Fatal("owned cleanup disturbed host configuration")
	}
}
