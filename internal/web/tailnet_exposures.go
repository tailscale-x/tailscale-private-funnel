package web

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
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
func (c *Control) nodePeers(w http.ResponseWriter, r *http.Request) {
	node, err := c.callerNode(r, "")
	if err != nil {
		http.Error(w, "Unknown or revoked node", 403)
		return
	}
	if node.Mode != "tailnet-gateway" {
		http.Error(w, "Tailnet gateway required", 403)
		return
	}
	var env report.Envelope
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&env) != nil {
		http.Error(w, "Invalid signed snapshot", 400)
		return
	}
	if env.NodeID != node.ID || env.PublicKey != node.PublicKey {
		http.Error(w, "Snapshot identity mismatch", 403)
		return
	}
	if _, _, err = report.Verify(env, time.Now(), 5*time.Minute); err != nil {
		http.Error(w, "Invalid snapshot signature or timestamp", 401)
		return
	}
	var snapshot nodeconfig.PeerSnapshot
	if json.Unmarshal(env.Payload, &snapshot) != nil || len(snapshot.Peers) > 2000 || len(snapshot.Results) > 20 || snapshot.ExpiresAt <= time.Now().Unix() || snapshot.ExpiresAt > env.Timestamp+300 {
		http.Error(w, "Invalid peer snapshot", 400)
		return
	}
	for _, source := range snapshot.Sources {
		if source != "docker" && source != "tailnet" {
			http.Error(w, "Invalid backend source", 400)
			return
		}
	}
	seen := map[string]bool{}
	for _, p := range snapshot.Peers {
		if p.ID == "" || seen[p.ID] || len(p.Addresses) == 0 || len(p.Addresses) > 8 {
			http.Error(w, "Invalid or duplicate peer", 400)
			return
		}
		seen[p.ID] = true
		for _, address := range p.Addresses {
			ip, e := netip.ParseAddr(address)
			if e != nil || !tailnetAddress(ip) {
				http.Error(w, "Peer addresses must be Tailscale addresses", 400)
				return
			}
		}
	}
	snapshot.Tags = append([]string{}, principal(r).Tags...)
	snapshot.Roles = principal(r).RoleSelectors()
	snapshot.ReceivedAt = time.Now().Unix()
	var probes []nodeconfig.Probe
	_ = c.Store.GetJSON("node_probes:"+node.ID, &probes)
	for _, result := range snapshot.Results {
		found := false
		for _, probe := range probes {
			if result.ID == probe.ID && result.DeviceID == probe.DeviceID && result.Port == probe.Port && probe.ExpiresAt > time.Now().Unix() && result.CheckedAt <= time.Now().Unix()+30 && result.CheckedAt >= env.Timestamp-30 {
				found = true
			}
		}
		if !found {
			http.Error(w, "Unknown or expired probe result", 400)
			return
		}
	}
	// Retain verified completed probes while an ordinary peer refresh is in flight.
	// Refreshes do not invalidate an administrator's still-fresh connection test.
	if len(snapshot.Results) == 0 {
		var previous nodeconfig.PeerSnapshot
		_ = c.Store.GetJSON("node_peers:"+node.ID, &previous)
		for _, result := range previous.Results {
			for _, probe := range probes {
				if result.ID == probe.ID && probe.ExpiresAt > time.Now().Unix() && result.CheckedAt > time.Now().Add(-2*time.Minute).Unix() {
					snapshot.Results = append(snapshot.Results, result)
				}
			}
		}
	}
	payload, _ := json.Marshal(snapshot)
	if err = c.Store.AcceptNodeSnapshot(node.ID, env.Nonce, env.PublicKey, "node_peers:"+node.ID, payload); err != nil {
		http.Error(w, "Snapshot rejected: replay or revoked identity", 409)
		return
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, principal(r).RoleSelectors())
	w.WriteHeader(204)
}
func tailnetAddress(ip netip.Addr) bool {
	return netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip)
}
func (c *Control) peersGet(w http.ResponseWriter, r *http.Request) {
	if !requestAccess(r).Admin {
		http.Error(w, "Administrator required", 403)
		return
	}
	node, err := c.Store.Node(chi.URLParam(r, "id"))
	if err != nil || node.Revoked || node.Mode != "tailnet-gateway" {
		http.Error(w, "Active tailnet gateway required", 400)
		return
	}
	snapshot, err := c.peerSnapshot(node.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, snapshot)
}
func findPeer(snapshot nodeconfig.PeerSnapshot, id string) (nodeconfig.Peer, bool) {
	for _, p := range snapshot.Peers {
		if p.ID == id {
			return p, true
		}
	}
	return nodeconfig.Peer{}, false
}
func (c *Control) probeCreate(w http.ResponseWriter, r *http.Request) {
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	node, err := c.Store.Node(chi.URLParam(r, "id"))
	if err != nil || node.Revoked || node.Mode != "tailnet-gateway" {
		http.Error(w, "Active tailnet gateway required", 400)
		return
	}
	var input struct {
		DeviceID string `json:"device_id"`
		Port     uint16 `json:"port"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || input.Port == 0 {
		http.Error(w, "Device and HTTP port required", 400)
		return
	}
	snapshot, err := c.peerSnapshot(node.ID)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if !contains(snapshot.Sources, "tailnet") {
		http.Error(w, "Wait for tailnet gateway peer discovery", 409)
		return
	}
	if _, ok := findPeer(snapshot, input.DeviceID); !ok {
		http.Error(w, "Select a device visible to this tailnet gateway", 400)
		return
	}
	var probes []nodeconfig.Probe
	_ = c.Store.GetJSON("node_probes:"+node.ID, &probes)
	pending := []nodeconfig.Probe{}
	for _, p := range probes {
		if p.ExpiresAt > time.Now().Unix() {
			pending = append(pending, p)
		}
	}
	if len(pending) >= 20 {
		http.Error(w, "Too many pending tests; wait for expiry", 429)
		return
	}
	var random [12]byte
	_, _ = rand.Read(random[:])
	probe := nodeconfig.Probe{ID: fmt.Sprintf("%x", random), DeviceID: input.DeviceID, Port: input.Port, ExpiresAt: time.Now().Add(2 * time.Minute).Unix()}
	pending = append(pending, probe)
	if err = c.Store.SetJSON("node_probes:"+node.ID, pending); err != nil {
		http.Error(w, "Unable to queue probe", 500)
		return
	}
	_ = c.Store.Audit("backend.probe:"+node.ID, identityFromRequest(r))
	writeJSON(w, probe)
}
func (c *Control) tailnetApplications(w http.ResponseWriter, r *http.Request) {
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, "Unable to read exposures", 503)
		return
	}
	out := []store.ApplicationRecord{}
	for _, app := range apps {
		if app.BackendSource == "tailnet" {
			out = append(out, app)
		}
	}
	writeJSON(w, out)
}
func (c *Control) saveTailnetExposure(w http.ResponseWriter, r *http.Request) {
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	var input struct {
		Name      string `json:"name"`
		GatewayID string `json:"gateway_id"`
		DeviceID  string `json:"device_id"`
		Hostname  string `json:"hostname"`
		Port      uint16 `json:"port"`
		Enabled   bool   `json:"enabled"`
		Revision  int64  `json:"revision"`
		ProbeID   string `json:"probe_id"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil || input.Port == 0 {
		http.Error(w, "Invalid exposure", 400)
		return
	}
	node, err := c.Store.Node(input.GatewayID)
	if err != nil || node.Revoked || node.Mode != "tailnet-gateway" {
		http.Error(w, "Active tailnet gateway required", 400)
		return
	}
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input.Hostname), "."))
	if policy.ValidateHostname(host) != nil {
		http.Error(w, "Valid public hostname required", 400)
		return
	}
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, "Unable to read exposures", 503)
		return
	}
	id := chi.URLParam(r, "id")
	var app store.ApplicationRecord
	for _, candidate := range apps {
		if candidate.ID == id {
			app = candidate
		}
		for _, h := range candidate.PublicHostnames {
			if h == host && candidate.ID != id {
				http.Error(w, "Hostname already assigned", 409)
				return
			}
		}
	}
	if id != "" && (app.ID == "" || app.BackendSource != "tailnet") {
		http.NotFound(w, r)
		return
	}
	if id != "" && app.PolicyRevision != input.Revision {
		http.Error(w, "Exposure changed; reload before saving", 409)
		return
	}
	// Disabling an unchanged assignment remains possible when its backend is offline.
	disabling := id != "" && !input.Enabled && app.BackendDeviceID == input.DeviceID && app.BackendPort == input.Port && len(app.PublicHostnames) == 1 && app.PublicHostnames[0] == host
	var peer nodeconfig.Peer
	if !disabling {
		snapshot, e := c.peerSnapshot(node.ID)
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		if !contains(snapshot.Sources, "tailnet") {
			http.Error(w, "Tailnet gateway peer discovery is unavailable", 409)
			return
		}
		var ok bool
		peer, ok = findPeer(snapshot, input.DeviceID)
		if !ok {
			http.Error(w, "Select a visible backend device", 400)
			return
		}
		if ok, _ := c.loadExposurePolicy().Allows(host, []string{node.ID, node.TailnetID}, snapshot.Roles); !ok {
			http.Error(w, "Exposure policy denies this hostname or tailnet gateway", 403)
			return
		}
		if _, e = c.managedZone(host); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		tested := false
		for _, result := range snapshot.Results {
			if result.ID == input.ProbeID && result.DeviceID == peer.ID && result.Port == input.Port && result.Reachable && result.CheckedAt > time.Now().Add(-2*time.Minute).Unix() {
				tested = true
			}
		}
		if !tested {
			http.Error(w, "Run a successful backend connection test before saving", 409)
			return
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
			http.Error(w, nameErr.Error(), 400)
			return
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
		http.Error(w, e.Error(), 428)
		return
	}
	service, e := client.CreateService(r.Context(), tailscale.Service{Name: app.ServiceName, DisplayName: host, Ports: []string{fmt.Sprintf("tcp:%d", app.ServicePort)}, Tags: []string{"tag:tailscale-private-funnel"}})
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	app.ServiceFQDN = selectServiceTarget(service.Addresses, app.ServiceName, client.Tailnet)
	err = c.Store.UpsertApplication(app)
	if err != nil {
		http.Error(w, "Unable to save exposure", 500)
		return
	}
	_ = c.Store.Audit("tailnet.exposure.save:"+app.ID, identityFromRequest(r))
	cleanupErr := c.ReconcilePolicy(r.Context())
	writeJSON(w, map[string]any{"application": app, "cleanup_pending": cleanupErr != nil, "message": "Saved. Tailnet gateway applies signed configuration on its next poll."})
}
func (c *Control) deleteTailnetExposure(w http.ResponseWriter, r *http.Request) {
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, "Unable to read exposures", 503)
		return
	}
	for _, app := range apps {
		if app.ID == chi.URLParam(r, "id") && app.BackendSource == "tailnet" {
			var input struct {
				Revision int64 `json:"revision"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Revision != app.PolicyRevision {
				http.Error(w, "Exposure changed; reload", 409)
				return
			}
			app.Enabled = false
			if err = c.Store.UpsertApplication(app); err != nil {
				http.Error(w, "Unable to disable exposure", 500)
				return
			}
			if err = c.ReconcilePolicy(r.Context()); err == nil {
				err = c.ReconcileServiceApprovals(r.Context())
			}
			if err != nil {
				http.Error(w, "Exposure disabled; cleanup pending. Retry deletion after reconciliation succeeds.", 502)
				return
			}
			if err = c.Store.DeleteApplication(app.ID); err != nil {
				http.Error(w, "Cleanup completed but local deletion failed", 500)
				return
			}
			_ = c.Store.Audit("tailnet.exposure.delete:"+app.ID, identityFromRequest(r))
			w.WriteHeader(204)
			return
		}
	}
	http.NotFound(w, r)
}
