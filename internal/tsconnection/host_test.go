package tsconnection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"tailscale.com/types/dnstype"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

type fakeHost struct {
	cfg     *ipn.ServeConfig
	prefs   ipn.Prefs
	device  tailcfg.StableNodeID
	failSet bool
}

func (f *fakeHost) Status(context.Context) (*ipnstate.Status, error) {
	return &ipnstate.Status{BackendState: "Running", Self: &ipnstate.PeerStatus{ID: f.device}}, nil
}
func (f *fakeHost) GetServeConfig(context.Context) (*ipn.ServeConfig, error) {
	b, _ := json.Marshal(f.cfg)
	var c ipn.ServeConfig
	_ = json.Unmarshal(b, &c)
	c.ETag = "current"
	return &c, nil
}
func (f *fakeHost) SetServeConfig(_ context.Context, c *ipn.ServeConfig) error {
	if f.failSet {
		return errors.New("etag mismatch")
	}
	if c.ETag != "current" {
		return errors.New("missing optimistic concurrency")
	}
	f.cfg = c
	return nil
}
func (f *fakeHost) GetPrefs(context.Context) (*ipn.Prefs, error) { p := f.prefs; return &p, nil }
func (f *fakeHost) EditPrefs(_ context.Context, p *ipn.MaskedPrefs) (*ipn.Prefs, error) {
	if !p.AdvertiseServicesSet {
		return nil, errors.New("unexpected host preference mutation")
	}
	f.prefs.AdvertiseServices = p.AdvertiseServices
	return &f.prefs, nil
}
func (f *fakeHost) UserDial(context.Context, string, string, uint16) (net.Conn, error) {
	return nil, errors.New("not needed")
}
func hostFixture(t *testing.T) (*Host, *fakeHost) {
	t.Helper()
	f := &fakeHost{device: "host-1", cfg: &ipn.ServeConfig{TCP: map[uint16]*ipn.TCPPortHandler{443: {TCPForward: "127.0.0.1:5000"}}}, prefs: ipn.Prefs{AdvertiseServices: []string{"svc:unrelated"}}}
	return &Host{API: f, DataDir: t.TempDir(), LockPath: filepath.Join(t.TempDir(), "daemon.lock")}, f
}
func TestBorrowedHostPreservesUnrelatedConfiguration(t *testing.T) {
	h, f := hostFixture(t)
	ctx := t.Context()
	target := "172.30.0.5:8080"
	if e := h.ForwardService(ctx, "svc:application", 8080, target); e != nil {
		t.Fatal(e)
	}
	if f.cfg.Services["svc:application"].TCP[8080].TCPForward != target {
		t.Fatal("not a direct container target")
	}
	if e := h.Close(); e != nil {
		t.Fatal(e)
	}
	if f.cfg.TCP[443].TCPForward != "127.0.0.1:5000" || !reflect.DeepEqual(f.prefs.AdvertiseServices, []string{"svc:unrelated"}) {
		t.Fatal("unrelated host configuration changed")
	}
	if f.device != "host-1" {
		t.Fatal("borrowed device changed")
	}
}
func TestHostRefusesForeignAndExternallyChangedServices(t *testing.T) {
	h, f := hostFixture(t)
	ctx := t.Context()
	if e := h.ForwardService(ctx, "svc:app", 80, "172.30.0.2:8080"); e != nil {
		t.Fatal(e)
	}
	other := &Host{API: f, DataDir: t.TempDir(), LockPath: h.LockPath}
	if e := other.ForwardService(ctx, "svc:app", 80, "172.30.0.3:8080"); e == nil {
		t.Fatal("competing ownership accepted")
	}
	f.cfg.Services["svc:app"].TCP[80].TCPForward = "external:9000"
	if e := h.RemoveService(ctx, "svc:app", 80); e == nil {
		t.Fatal("external edit overwritten")
	}
	if f.cfg.Services["svc:app"].TCP[80].TCPForward != "external:9000" {
		t.Fatal("foreign configuration touched")
	}
}
func TestHostDetectsDeviceReplacementAndWriteConflict(t *testing.T) {
	h, f := hostFixture(t)
	if _, e := h.Up(t.Context()); e != nil {
		t.Fatal(e)
	}
	f.device = "replacement"
	if _, e := h.Up(t.Context()); e == nil {
		t.Fatal("replacement identity accepted")
	}
	f.device = "host-1"
	f.failSet = true
	if e := h.ForwardService(t.Context(), "svc:app", 80, "172.30.0.2:80"); e == nil {
		t.Fatal("conflicting write accepted")
	}
	b, _ := os.ReadFile(h.manifestPath())
	var owned map[string]ownedService
	_ = json.Unmarshal(b, &owned)
	if len(owned) != 0 {
		t.Fatal("failed update falsely acquired ownership")
	}
}
func TestHostControlFailsClosedBeforeHostMutation(t *testing.T) {
	h, f := hostFixture(t)
	before, _ := json.Marshal(f.cfg)
	if _, err := ListenControl(t.Context(), h, "svc:control", 8080); !errors.Is(err, ErrHostControlUnsupported) {
		t.Fatal(err)
	}
	after, _ := json.Marshal(f.cfg)
	if string(before) != string(after) {
		t.Fatal("unsupported control changed host Serve state")
	}
	if _, err := os.Stat(h.manifestPath()); !os.IsNotExist(err) {
		t.Fatal("unsupported control recorded forwarding ownership", err)
	}
}

func (f *fakeHost) QueryDNS(context.Context, string, string) ([]byte, []*dnstype.Resolver, error) {
	return nil, nil, fmt.Errorf("no fixture DNS")
}

func TestBorrowedHostBindingSurvivesProcessReplacement(t *testing.T) {
	h, f := hostFixture(t)
	if err := h.ForwardService(t.Context(), "svc:app", 80, "172.30.0.2:8080"); err != nil {
		t.Fatal(err)
	}
	replacement := &Host{API: f, DataDir: h.DataDir, LockPath: h.LockPath}
	f.device = "other-device"
	if err := replacement.RemoveService(t.Context(), "svc:app", 80); err == nil {
		t.Fatal("replacement device modified prior device resources")
	}
	if f.cfg.Services["svc:app"].TCP[80] == nil {
		t.Fatal("binding mismatch removed configuration")
	}
	f.device = "host-1"
	if err := replacement.RemoveService(t.Context(), "svc:app", 80); err != nil {
		t.Fatal(err)
	}
}

func TestHostMultiplePortsWithdrawAdvertisementAfterLastPort(t *testing.T) {
	h, f := hostFixture(t)
	for _, p := range []uint16{80, 8080} {
		if err := h.ForwardService(t.Context(), "svc:app", p, "172.30.0.2:8080"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.RemoveService(t.Context(), "svc:app", 80); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.prefs.AdvertiseServices, []string{"svc:unrelated", "svc:app"}) {
		t.Fatal("remaining port lost advertisement")
	}
	if err := h.RemoveService(t.Context(), "svc:app", 8080); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.prefs.AdvertiseServices, []string{"svc:unrelated"}) {
		t.Fatal("last port left advertisement or removed unrelated one")
	}
}
