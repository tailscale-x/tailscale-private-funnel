package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

// Kubernetes desired state is signed and identity-bound before it can create
// application Services. Node-provided Service names and targets are discarded.
func (c *Control) kubernetesDesired(w http.ResponseWriter, r *http.Request) {
	node, err := c.callerNode(r, "k8s-gateway")
	if err != nil {
		http.Error(w, "Kubernetes gateway required", 403)
		return
	}
	var env report.Envelope
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if dec.Decode(&env) != nil || env.NodeID != node.ID || env.PublicKey != node.PublicKey {
		http.Error(w, "signed gateway snapshot required", 403)
		return
	}
	if _, _, err = report.Verify(env, time.Now(), 5*time.Minute); err != nil {
		http.Error(w, err.Error(), 401)
		return
	}
	var snapshot nodeconfig.KubernetesSnapshot
	payloadDecoder := json.NewDecoder(bytes.NewReader(env.Payload))
	payloadDecoder.DisallowUnknownFields()
	if payloadDecoder.Decode(&snapshot) != nil || snapshot.ClusterID == "" || len(snapshot.ClusterID) > 128 || len(snapshot.Hosts) > 500 || snapshot.ExpiresAt <= time.Now().Unix() || snapshot.ExpiresAt > env.Timestamp+300 {
		http.Error(w, "invalid Kubernetes snapshot", 400)
		return
	}
	seen := map[string]bool{}
	for _, host := range snapshot.Hosts {
		if policy.ValidateHostname(host.Hostname) != nil || seen[host.Hostname] || len(host.Rules) == 0 || len(host.Rules) > 100 {
			http.Error(w, "invalid or duplicate hostname", 400)
			return
		}
		seen[host.Hostname] = true
		if ok, _ := c.loadExposurePolicy().Allows(host.Hostname, []string{node.ID, node.TailnetID}, principal(r).RoleSelectors()); !ok {
			http.Error(w, "hostname denied by exposure policy", 403)
			return
		}
		if _, err := c.managedZone(host.Hostname); err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
		for _, rule := range host.Rules {
			if err := proxy.ValidateHTTPRule(rule, false); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
	}
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, h := range snapshot.Hosts {
		for _, a := range apps {
			for _, name := range a.PublicHostnames {
				if name == h.Hostname && (a.BackendSource != "kubernetes" || !contains(a.AllowedGatewayNodes, node.ID)) {
					http.Error(w, "hostname owned by another gateway or source", 409)
					return
				}
			}
		}
	}
	// Consume the nonce before external mutations; retry uses a fresh envelope.
	if err = c.Store.AcceptNodeSnapshot(node.ID, env.Nonce, env.PublicKey, "node_kubernetes:"+node.ID, env.Payload); err != nil {
		http.Error(w, "snapshot rejected or replayed", 409)
		return
	}
	ts, err := c.tailscaleRuntimeClient()
	if err != nil {
		http.Error(w, err.Error(), 428)
		return
	}
	desired := map[string]bool{}
	for _, h := range snapshot.Hosts {
		id := fmt.Sprintf("k8s-%x", sha256.Sum256([]byte(node.ID+":"+snapshot.ClusterID+":"+h.Hostname)))[:28]
		desired[id] = true
		backends := map[string]proxy.Backend{}
		for ri := range h.Rules {
			for bi, b := range h.Rules[ri].Backends {
				assigned, ok := backends[b.Key]
				if !ok {
					assigned = b
					assigned.ServiceName = fmt.Sprintf("svc:tpf-k8s-%x", sha256.Sum256([]byte(node.ID+":"+snapshot.ClusterID+":"+b.Key)))[:40]
					service, e := ts.CreateService(r.Context(), tailscale.Service{Name: assigned.ServiceName, DisplayName: h.Hostname, Ports: []string{fmt.Sprintf("tcp:%d", b.Port)}, Tags: []string{"tag:tailscale-private-funnel"}})
					if e != nil {
						http.Error(w, e.Error(), 502)
						return
					}
					assigned.Service = selectServiceTarget(service.Addresses, assigned.ServiceName, ts.Tailnet)
					backends[b.Key] = assigned
				}
				assigned.Weight = b.Weight
				h.Rules[ri].Backends[bi] = assigned
			}
		}
		keys := []string{}
		for key := range backends {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		all := []proxy.Backend{}
		for _, key := range keys {
			all = append(all, backends[key])
		}
		a := store.ApplicationRecord{ID: id, Name: h.Hostname, BackendSource: "kubernetes", Enabled: true, PublicHostnames: []string{h.Hostname}, AllowedGatewayNodes: []string{node.ID}, HTTPRules: h.Rules, HTTPBackends: all, PolicyRevision: int64(c.loadExposurePolicy().Revision), ServiceName: "svc:tpf-redirect-" + id, ServiceFQDN: "redirect.invalid", ServicePort: 80, BackendPort: 80}
		if len(all) > 0 {
			a.ServiceName = all[0].ServiceName
			a.ServiceFQDN = all[0].Service
			a.ServicePort = all[0].Port
			a.BackendPort = all[0].Port
		}
		if err = c.Store.UpsertApplication(a); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	for _, a := range apps {
		if a.BackendSource == "kubernetes" && applicationAllows(a, node) && !desired[a.ID] {
			a.Enabled = false
			if err = c.Store.UpsertApplication(a); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
	}
	_ = c.Store.Audit("kubernetes.desired:"+node.ID, "node:"+node.TailnetID)
	c.nodeConfig(w, r)
}

func (c *Control) publishedGatewayHosts() []string {
	set := map[string]bool{}
	apps, err := c.Store.Applications()
	if err != nil {
		return nil
	}
	for _, n := range c.loadBalancers() {
		var sent struct {
			Revision string        `json:"revision"`
			Routes   []proxy.Route `json:"routes"`
		}
		var ack struct {
			Revision string `json:"revision"`
			At       int64  `json:"at"`
		}
		if c.Store.GetJSON("node_sent_config:"+n.ID, &sent) != nil || c.Store.GetJSON("node_config_ack:"+n.ID, &ack) != nil || sent.Revision != ack.Revision || ack.At < time.Now().Add(-5*time.Minute).Unix() {
			continue
		}
		for _, r := range sent.Routes {
			for _, app := range apps {
				if app.Enabled && contains(app.PublicHostnames, r.Hostname) && app.ServiceFQDN == r.Service && app.ServicePort == r.Port && nodeconfig.Revision(app.HTTPRules) == nodeconfig.Revision(r.Rules) {
					set[r.Hostname] = true
				}
			}
		}
	}
	out := []string{}
	for host := range set {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}
