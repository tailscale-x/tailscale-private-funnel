package web

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func (c *Control) peerSnapshot(id string) (nodeconfig.PeerSnapshot, error) {
	var snapshot nodeconfig.PeerSnapshot
	if err := c.Store.GetJSON("node_peers:"+id, &snapshot); err != nil {
		return snapshot, fmt.Errorf("tailnet gateway has not reported peers")
	}
	if snapshot.ExpiresAt <= time.Now().Unix() || snapshot.ReceivedAt < time.Now().Add(-5*time.Minute).Unix() {
		return snapshot, fmt.Errorf("tailnet gateway peer information is stale; wait for a fresh report")
	}
	return snapshot, nil
}

func tailnetAddress(ip netip.Addr) bool {
	return netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip)
}

func findPeer(snapshot nodeconfig.PeerSnapshot, id string) (nodeconfig.Peer, bool) {
	for _, p := range snapshot.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return nodeconfig.Peer{}, false
}

type tailnetExposureInput struct {
	Name      string `json:"name"`
	GatewayID string `json:"gateway_id"`
	DeviceID  string `json:"device_id"`
	Hostname  string `json:"hostname"`
	Port      uint16 `json:"port"`
	Enabled   bool   `json:"enabled"`
	Revision  int64  `json:"revision"`
	ProbeID   string `json:"probe_id"`
}

func (c *Control) saveTailnetExposureOperation(ctx context.Context, id string, input tailnetExposureInput, actor string) (store.ApplicationRecord, error) {
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	node, err := c.Store.Node(input.GatewayID)
	if err != nil || node.Revoked || node.Mode != "tailnet-gateway" {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New("Active tailnet gateway required"))
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input.Hostname), "."))
	if policy.ValidateHostname(host) != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New("Valid public hostname required"))
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeUnavailable, errors.New("Unable to read exposures"))
	}
	var app store.ApplicationRecord
	for _, candidate := range apps {
		if candidate.ID == id {
			app = candidate
		}
		for _, h := range candidate.PublicHostnames {
			if h == host && candidate.ID != id {
				return store.ApplicationRecord{}, rpcFailure(connect.CodeAborted, errors.New("Hostname already assigned"))
			}
		}
	}
	if id != "" && (app.ID == "" || app.BackendSource != "tailnet") {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeNotFound, errors.New("Resource not found"))
	}
	if id != "" && app.PolicyRevision != input.Revision {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeAborted, errors.New("Exposure changed; reload before saving"))
	}
	// Disabling an unchanged assignment remains possible when its backend is offline.
	disabling := id != "" && !input.Enabled && app.BackendDeviceID == input.DeviceID && app.BackendPort == input.Port && len(app.PublicHostnames) == 1 && app.PublicHostnames[0] == host
	var peer nodeconfig.Peer
	if !disabling {
		snapshot, e := c.peerSnapshot(node.ID)
		if e != nil {
			return store.ApplicationRecord{}, rpcFailure(connect.CodeAborted, errors.New(e.Error()))
		}
		if !contains(snapshot.Sources, "tailnet") {
			return store.ApplicationRecord{}, rpcFailure(connect.CodeAborted, errors.New("Tailnet gateway peer discovery is unavailable"))
		}
		var ok bool
		peer, ok = findPeer(snapshot, input.DeviceID)
		if !ok {
			return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New("Select a visible backend device"))
		}
		if ok, _ := c.loadExposurePolicy().Allows(host, []string{node.ID, node.TailnetID}, snapshot.Roles); !ok {
			return store.ApplicationRecord{}, rpcFailure(connect.CodePermissionDenied, errors.New("Exposure policy denies this hostname or tailnet gateway"))
		}
		if _, e = c.managedZone(host); e != nil {
			return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New(e.Error()))
		}
		tested := false
		for _, result := range snapshot.Results {
			if result.ID == input.ProbeID && result.DeviceID == peer.ID && result.Port == input.Port && result.Reachable && result.CheckedAt > time.Now().Add(-2*time.Minute).Unix() {
				tested = true
			}
		}
		if !tested {
			return store.ApplicationRecord{}, rpcFailure(connect.CodeAborted, errors.New("Run a successful backend connection test before saving"))
		}
	}
	if id == "" {
		var random [12]byte
		_, _ = rand.Read(random[:])
		id = "remote-" + fmt.Sprintf("%x", random)
		app = store.ApplicationRecord{ID: id, ServiceName: "direct:" + id, BackendSource: "tailnet"}
	}
	if !disabling {
		app.ServiceFQDN = ""
		app.ServicePort = 0
		app.BackendPort = input.Port
		app.BackendDeviceID = peer.ID
		app.BackendAddresses = nil
		app.PublicHostnames = []string{host}
		app.AllowedGatewayNodes = nil
	}
	if input.Name != "" {
		name, nameErr := store.ValidateName(input.Name)
		if nameErr != nil {
			return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New(nameErr.Error()))
		}
		app.Name = name
	} else if app.Name == "" {
		app.Name = host
	}
	app.Enabled = input.Enabled
	app.PolicyRevision = time.Now().UnixMicro()
	app.ServiceName = "svc:tailscale-private-funnel-app-" + app.ID
	app.ServicePort = app.BackendPort
	app.AllowedGatewayNodes = []string{node.ID}
	client, e := c.tailscaleRuntimeClient()
	if e != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeFailedPrecondition, errors.New(e.Error()))
	}
	service, e := client.CreateService(ctx, tailscale.Service{Name: app.ServiceName, DisplayName: host, Ports: []string{fmt.Sprintf("tcp:%d", app.ServicePort)}, Tags: []string{"tag:tailscale-private-funnel"}})
	if e != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeUnavailable, errors.New(e.Error()))
	}
	app.ServiceFQDN = selectServiceTarget(service.Addresses, app.ServiceName, client.Tailnet)
	err = c.Store.UpsertApplication(app)
	if err != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeInternal, errors.New("Unable to save exposure"))
	}
	_ = c.Store.Audit("tailnet.exposure.save:"+app.ID, actor)
	_ = c.ReconcilePolicy(ctx)
	return app, nil
}
