package tsconnection

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/types/dnstype"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

type HostAPI interface {
	QueryDNS(context.Context, string, string) ([]byte, []*dnstype.Resolver, error)
	Status(context.Context) (*ipnstate.Status, error)
	GetServeConfig(context.Context) (*ipn.ServeConfig, error)
	SetServeConfig(context.Context, *ipn.ServeConfig) error
	GetPrefs(context.Context) (*ipn.Prefs, error)
	EditPrefs(context.Context, *ipn.MaskedPrefs) (*ipn.Prefs, error)
	UserDial(context.Context, string, string, uint16) (net.Conn, error)
}
type ownedService struct {
	Target           string `json:"target"`
	ProxyProtocol    int    `json:"proxy_protocol"`
	AdvertisedBefore bool   `json:"advertised_before"`
}
type Host struct {
	Client            *local.Client
	API               HostAPI
	DataDir, LockPath string
	mu                sync.Mutex
	deviceID          string
}

func NewHost(socket, dataDir string) *Host {
	c := &local.Client{Socket: socket, UseSocketOnly: socket != ""}
	return &Host{Client: c, API: c, DataDir: dataDir}
}
func (h *Host) LocalClient() (*local.Client, error) {
	if h.Client == nil {
		return nil, errors.New("local Tailscale client unavailable")
	}
	return h.Client, nil
}
func (h *Host) Up(ctx context.Context) (*ipnstate.Status, error) {
	st, e := h.API.Status(ctx)
	if e != nil {
		return nil, fmt.Errorf("host Tailscale unavailable: %w", e)
	}
	if st.BackendState != "Running" || st.Self == nil || st.Self.ID == "" {
		return nil, errors.New("host Tailscale must already be connected")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id := string(st.Self.ID)
	if e = os.MkdirAll(h.DataDir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(h.DataDir, "host-device-id")
	recorded, readErr := os.ReadFile(path)
	if readErr == nil && strings.TrimSpace(string(recorded)) != id {
		return nil, errors.New("borrowed Tailscale device differs from this component's binding; recreate the component explicitly")
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		return nil, readErr
	}
	if os.IsNotExist(readErr) {
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.WriteString(id)
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}

	if h.deviceID != "" && h.deviceID != id {
		return nil, errors.New("host Tailscale device changed; refusing replacement identity")
	}
	h.deviceID = id
	return st, nil
}
func tailnetAddress(ip netip.Addr) bool {
	return netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip)
}
func (h *Host) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	st, e := h.Up(ctx)
	if e != nil {
		return nil, e
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("host transport supports TCP only")
	}
	host, portText, e := net.SplitHostPort(address)
	if e != nil {
		return nil, e
	}
	port, e := strconv.ParseUint(portText, 10, 16)
	if e != nil || port == 0 {
		return nil, errors.New("valid tailnet TCP port required")
	}
	var addresses []netip.Addr
	if ip, e := netip.ParseAddr(host); e == nil {
		addresses = append(addresses, ip)
	} else {
		suffix := ""
		if st.CurrentTailnet != nil {
			suffix = strings.TrimSuffix(st.CurrentTailnet.MagicDNSSuffix, ".")
		}
		name := strings.TrimSuffix(strings.ToLower(host), ".")
		if suffix == "" || !strings.HasSuffix(name, "."+strings.ToLower(suffix)) {
			return nil, errors.New("host transport requires a verified MagicDNS name or Tailscale address")
		}
		for _, typ := range []string{"AAAA", "A"} {
			wire, _, e := h.API.QueryDNS(ctx, name, typ)
			if e != nil {
				continue
			}
			var msg dnsmessage.Message
			if e = msg.Unpack(wire); e != nil {
				continue
			}
			for _, a := range msg.Answers {
				switch body := a.Body.(type) {
				case *dnsmessage.AResource:
					addresses = append(addresses, netip.AddrFrom4(body.A))
				case *dnsmessage.AAAAResource:
					addresses = append(addresses, netip.AddrFrom16(body.AAAA))
				}
			}
		}
	}
	var last error
	for _, ip := range addresses {
		if !tailnetAddress(ip) {
			return nil, errors.New("MagicDNS returned a non-Tailscale address; host-network fallback refused")
		}
		if network == "tcp4" && !ip.Is4() || network == "tcp6" && !ip.Is6() {
			continue
		}
		conn, e := h.API.UserDial(ctx, network, ip.String(), uint16(port))
		if e == nil {
			return conn, nil
		}
		last = e
	}
	if last != nil {
		return nil, last
	}
	return nil, errors.New("no usable Tailscale address for destination")
}

func (h *Host) HTTPClient() *http.Client { return HTTPClient(h.Dial) }
func (h *Host) ListenService(string, tsnet.ServiceMode) (net.Listener, error) {
	return nil, errors.New("host Services require direct forwarding targets")
}
func (h *Host) Listen(string, string) (net.Listener, error) {
	return nil, errors.New("host control requires a protected Service listener")
}
func (h *Host) ForwardService(ctx context.Context, name string, port uint16, target string) error {
	return h.forward(ctx, name, port, target, 0)
}
func (h *Host) manifestPath() string { return filepath.Join(h.DataDir, "host-services.json") }
func (h *Host) locked(fn func(map[string]ownedService) error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if e := os.MkdirAll(h.DataDir, 0700); e != nil {
		return e
	}
	lock := h.LockPath
	if lock == "" {
		socket := "default"
		if h.Client != nil {
			socket = h.Client.Socket
		}
		dir := "/run/tailscale-private-funnel"
		if e := os.MkdirAll(dir, 0700); e != nil {
			return fmt.Errorf("host-scoped lock directory unavailable; prepare the package RuntimeDirectory: %w", e)
		}
		lock = filepath.Join(dir, fmt.Sprintf("host-%x.lock", sha256.Sum256([]byte(socket))))
	}
	fd, e := syscall.Open(lock, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), lock)
	defer f.Close()
	if e = syscall.Flock(fd, syscall.LOCK_EX); e != nil {
		return e
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	owned := map[string]ownedService{}
	b, e := os.ReadFile(h.manifestPath())
	if e == nil {
		if e = json.Unmarshal(b, &owned); e != nil {
			return fmt.Errorf("corrupt host Service ownership: %w", e)
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = fn(owned); e != nil {
		return e
	}
	b, e = json.Marshal(owned)
	if e != nil {
		return e
	}
	tmp := h.manifestPath() + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, h.manifestPath())
}
func serviceKey(name string, port uint16) string { return name + ":" + strconv.Itoa(int(port)) }
func matches(cfg *ipn.ServeConfig, name tailcfg.ServiceName, port uint16, o ownedService) bool {
	if cfg == nil || cfg.Services[name] == nil {
		return false
	}
	p := cfg.Services[name].TCP[port]
	return p != nil && reflect.DeepEqual(p, &ipn.TCPPortHandler{TCPForward: o.Target, ProxyProtocol: o.ProxyProtocol})
}
func (h *Host) forward(ctx context.Context, name string, port uint16, target string, proxy int) error {
	if !strings.HasPrefix(name, "svc:") || port == 0 || target == "" {
		return errors.New("Service, port, and direct target required")
	}
	if _, e := h.Up(ctx); e != nil {
		return e
	}
	return h.locked(func(owned map[string]ownedService) error {
		cfg, e := h.API.GetServeConfig(ctx)
		if e != nil {
			return e
		}
		if cfg == nil {
			cfg = &ipn.ServeConfig{}
		}
		key := serviceKey(name, port)
		old, ours := owned[key]
		serviceOwned := ours
		inheritedBefore := false
		for existingKey, value := range owned {
			if strings.HasPrefix(existingKey, name+":") {
				serviceOwned = true
				inheritedBefore = value.AdvertisedBefore
				break
			}
		}
		if cfg.Services[tailcfg.ServiceName(name)] != nil && !serviceOwned {
			return errors.New("Service belongs to another host configuration owner; use a distinct application Service")
		}

		sn := tailcfg.ServiceName(name)
		if svc := cfg.Services[sn]; svc != nil {
			if existing := svc.TCP[port]; existing != nil && (!ours || !matches(cfg, sn, port, old)) {
				return errors.New("Service forwarding is owned or changed by another actor")
			}
		}
		prefs, e := h.API.GetPrefs(ctx)
		if e != nil {
			return e
		}
		before := false
		for _, s := range prefs.AdvertiseServices {
			before = before || s == name
		}
		if serviceOwned {
			before = inheritedBefore
		}
		if ours {
			before = old.AdvertisedBefore
		}
		// Write ownership intent before mutation so an interrupted update is recoverable.
		next := ownedService{target, proxy, before}
		owned[key] = next
		b, _ := json.Marshal(owned)
		if e = os.WriteFile(h.manifestPath(), b, 0600); e != nil {
			return e
		}
		cfg.SetTCPForwardingForService(port, target, false, sn, proxy, "")
		if e = h.API.SetServeConfig(ctx, cfg); e != nil {
			if ours {
				owned[key] = old
			} else {
				delete(owned, key)
			}
			b, _ = json.Marshal(owned)
			_ = os.WriteFile(h.manifestPath(), b, 0600)
			return e
		}
		found := false
		for _, s := range prefs.AdvertiseServices {
			found = found || s == name
		}
		if !found {
			_, e = h.API.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{AdvertiseServices: append(prefs.AdvertiseServices, name)}, AdvertiseServicesSet: true})
		}
		return e
	})
}
func (h *Host) RemoveService(ctx context.Context, name string, port uint16) error {
	if _, err := h.Up(ctx); err != nil {
		return err
	}
	return h.locked(func(owned map[string]ownedService) error {
		key := serviceKey(name, port)
		old, ok := owned[key]
		if !ok {
			return nil
		}
		cfg, e := h.API.GetServeConfig(ctx)
		if e != nil {
			return e
		}
		sn := tailcfg.ServiceName(name)
		if cfg != nil && cfg.Services[sn] != nil && cfg.Services[sn].TCP[port] != nil {
			if !matches(cfg, sn, port, old) {
				return errors.New("owned Service was externally changed; refusing removal")
			}
			cfg.RemoveTCPForwarding(sn, port)
			if e = h.API.SetServeConfig(ctx, cfg); e != nil {
				return e
			}
		}
		if !old.AdvertisedBefore && (cfg == nil || cfg.Services[sn] == nil) {
			p, e := h.API.GetPrefs(ctx)
			if e != nil {
				return e
			}
			var keep []string
			for _, s := range p.AdvertiseServices {
				if s != name {
					keep = append(keep, s)
				}
			}
			if _, e = h.API.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{AdvertiseServices: keep}, AdvertiseServicesSet: true}); e != nil {
				return e
			}
		}
		delete(owned, key)
		return nil
	})
}
func (h *Host) Close() error {
	b, e := os.ReadFile(h.manifestPath())
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e = h.Up(ctx); e != nil {
		return e
	}
	var owned map[string]ownedService
	if e = json.Unmarshal(b, &owned); e != nil {
		return e
	}
	var errs []error
	for k := range owned {
		idx := strings.LastIndex(k, ":")
		if idx < 4 {
			return errors.New("invalid host Service ownership manifest")
		}
		p, e := strconv.ParseUint(k[idx+1:], 10, 16)
		if e != nil || p == 0 {
			return errors.New("invalid owned Service port")
		}
		if e = func() error {
			bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return h.RemoveService(bounded, k[:idx], uint16(p))
		}(); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
