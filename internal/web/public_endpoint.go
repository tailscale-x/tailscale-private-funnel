package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type publicEndpoint struct {
	Namespace  string                     `json:"namespace"`
	Service    string                     `json:"service"`
	UID        string                     `json:"uid"`
	ObservedAt int64                      `json:"observed_at"`
	Ingress    []core.LoadBalancerIngress `json:"ingress"`
}

func (c *Control) publicEndpoint(w http.ResponseWriter, r *http.Request) {
	n, err := c.callerNode(r, "loadbalancer")
	if err != nil {
		http.Error(w, "enrolled load balancer required", 403)
		return
	}
	d, err := c.nodeDeployment(n.ID)
	if err != nil || d.AddressSource != "kubernetes" {
		http.Error(w, "Kubernetes address discovery not authorized for this node", 403)
		return
	}
	var env report.Envelope
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	dec.DisallowUnknownFields()
	if dec.Decode(&env) != nil || env.NodeID != n.ID || env.PublicKey != n.PublicKey {
		http.Error(w, "signed endpoint required", 403)
		return
	}
	if _, _, err = report.Verify(env, time.Now(), time.Minute); err != nil {
		http.Error(w, "invalid endpoint signature or timestamp", 401)
		return
	}
	var in publicEndpoint
	dec = json.NewDecoder(strings.NewReader(string(env.Payload)))
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil || in.Namespace != d.KubernetesNamespace || in.Service != d.PublicService || in.UID == "" || len(in.UID) > 128 || len(in.Ingress) > 16 || in.ObservedAt < time.Now().Add(-time.Minute).Unix() || in.ObservedAt > time.Now().Add(time.Minute).Unix() {
		http.Error(w, "invalid or mismatched public Service observation", 400)
		return
	}
	// Binding and nonce consumption are serialized so the first Service UID cannot race.
	c.endpointMu.Lock()
	defer c.endpointMu.Unlock()
	var previous publicEndpoint
	err = c.Store.GetJSON("node_public_endpoint:"+n.ID, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Service binding unavailable", 503)
		return
	}
	if err == nil && previous.UID != in.UID {
		http.Error(w, "Service identity changed; administrator reenrollment required", 409)
		return
	}
	if err = c.Store.AcceptNodeSnapshot(n.ID, env.Nonce, env.PublicKey, "node_public_endpoint:"+n.ID, env.Payload); err != nil {
		http.Error(w, "endpoint replayed or unavailable", 409)
		return
	}
	_ = c.Store.Audit("loadbalancer.public-endpoint:"+n.ID, "node:"+n.TailnetID)
	err = c.reconcileLoadBalancerNode(r.Context(), n)
	writeJSON(w, map[string]any{"accepted": true, "pending": err != nil, "message": diagnosticValueError(err)})
}
func diagnosticValueError(err error) string {
	if err == nil {
		return "Public Service address reconciled"
	}
	return err.Error()
}
func (c *Control) kubernetesAddresses(ctx context.Context, n store.NodeRecord, d nodeDeployment) ([]string, error) {
	var p publicEndpoint
	if err := c.Store.GetJSON("node_public_endpoint:"+n.ID, &p); err != nil {
		return nil, fmt.Errorf("Kubernetes LoadBalancer allocation pending; retaining owned DNS")
	}
	if p.Namespace != d.KubernetesNamespace || p.Service != d.PublicService || p.ObservedAt < time.Now().Add(-time.Minute).Unix() {
		return nil, fmt.Errorf("Kubernetes Service observation stale or mismatched; retaining owned DNS")
	}
	ips := []string{}
	for _, i := range p.Ingress {
		if i.IP != "" {
			ips = append(ips, i.IP)
		}
		if i.Hostname != "" {
			host := strings.TrimSuffix(i.Hostname, ".")
			if policy.ValidateHostname(host) != nil {
				return nil, fmt.Errorf("invalid Service hostname; retaining owned DNS")
			}
			lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
			resolve := c.PublicLookup
			if resolve == nil {
				resolve = net.DefaultResolver.LookupIPAddr
			}
			addresses, e := resolve(lookup, host)
			cancel()
			if e != nil {
				return nil, fmt.Errorf("Service hostname resolution failed; retaining owned DNS")
			}
			for _, a := range addresses {
				ips = append(ips, a.IP.String())
			}
		}
	}
	ips = dns.PublicIPv4Endpoints(ips)
	_ = c.Store.SetJSON("node_endpoints:"+n.ID, map[string]any{"public_ipv4": ips, "checked_at": time.Now().Unix(), "source": "kubernetes", "namespace": p.Namespace, "service": p.Service, "uid": p.UID})
	if len(ips) == 0 {
		return nil, fmt.Errorf("Kubernetes LoadBalancer has no usable public IPv4 address; retaining owned DNS")
	}
	return ips, nil
}

// bindPublicService is an explicit administrator migration, never a node report.
func (c *Control) bindPublicService(w http.ResponseWriter, r *http.Request) {
	if !requestAccess(r).Admin {
		http.Error(w, "administrator required", 403)
		return
	}
	n, e := c.Store.Node(chi.URLParam(r, "id"))
	if e != nil || n.Mode != "loadbalancer" || n.Revoked {
		http.Error(w, "active load balancer required", 400)
		return
	}
	var in struct {
		Namespace string `json:"namespace"`
		Service   string `json:"service"`
		Stopped   bool   `json:"confirm_stopped"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	label := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	if dec.Decode(&in) != nil || !in.Stopped || len(in.Namespace) > 63 || len(in.Service) > 63 || !label.MatchString(in.Namespace) || !label.MatchString(in.Service) {
		http.Error(w, "valid Service binding and stopped-state confirmation required", 400)
		return
	}
	c.endpointMu.Lock()
	defer c.endpointMu.Unlock()
	d, e := c.nodeDeployment(n.ID)
	if e != nil {
		http.Error(w, "deployment unavailable", 503)
		return
	}
	d.AddressSource = "kubernetes"
	d.DeploymentTarget = "kubernetes"
	d.KubernetesNamespace = in.Namespace
	d.PublicService = in.Service
	if e = c.Store.BindPublicService(n.ID, d); e != nil {
		http.Error(w, "binding failed", 500)
		return
	}
	_ = c.Store.Audit("node.public-service.bind:"+n.ID, identityFromRequest(r))
	writeJSON(w, map[string]any{"bound": true, "pending": true})
}
