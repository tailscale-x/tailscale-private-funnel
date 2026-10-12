package web

import (
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"net/http"
	"strings"
	"time"
)

// Edge reports authenticate the reporting edge, never an ingress-supplied edge ID.
func (c *Control) saveDirectReport(w http.ResponseWriter, r *http.Request, env report.Envelope, node store.NodeRecord) {
	if node.Revoked || node.TailnetID == "" || principal(r).NodeID != node.TailnetID || env.PublicKey != node.PublicKey {
		http.Error(w, "load balancer identity mismatch", 403)
		return
	}
	var p struct {
		Exposures []dns.IngressExposure `json:"exposures"`
		ExpiresAt int64                 `json:"expires_at"`
	}
	if json.Unmarshal(env.Payload, &p) != nil || p.ExpiresAt <= time.Now().Unix() || p.ExpiresAt > env.Timestamp+300 {
		http.Error(w, "invalid report expiry", 400)
		return
	}
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	snapshot, _ := c.peerSnapshot(node.ID)
	for _, e := range p.Exposures {
		valid := false
		for _, app := range apps {
			if !app.Enabled || app.ID != e.ApplicationID || app.BackendPort != e.Port || !contains(app.PublicHostnames, e.PublicHostname) || !c.selected(e.PublicHostname, node.ID) {
				continue
			}
			if app.BackendSource == "tailnet" {
				if app.BackendDeviceID != e.BackendDeviceID || e.ServiceName != "" || e.ContainerID != "" {
					continue
				}
				if e.Reachable {
					peer, ok := findPeer(snapshot, e.BackendDeviceID)
					if !ok || !contains(peer.Addresses, e.BackendIP) {
						continue
					}
				}
				if ok, _ := c.loadExposurePolicy().Allows(e.PublicHostname, node.ID, nil, principal(r).RoleSelectors()); !ok {
					continue
				}
			} else if app.ServiceName != e.ServiceName || e.BackendDeviceID != "" || e.ContainerID != "" {
				continue
			}
			valid = true
			break
		}
		if !valid {
			http.Error(w, "exposure is not assigned to this load balancer or backend identity changed", 403)
			return
		}
	}
	encoded, _ := json.Marshal(env)
	if err = c.Store.AcceptReport(node.ID, env.Nonce, encoded, env.PublicKey); err != nil {
		status := 500
		if strings.Contains(err.Error(), "nonce already") {
			status = 409
		}
		http.Error(w, err.Error(), status)
		return
	}
	_ = c.Store.SetJSON("node_policy_roles:"+node.ID, principal(r).RoleSelectors())
	if err = c.reconcileExposures(r.Context()); err != nil {
		http.Error(w, "Report accepted; DNS cleanup pending: "+err.Error(), 502)
		return
	}
	w.WriteHeader(202)
}
