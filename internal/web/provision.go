package web

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

// provisionApplication turns a verified gateway label into a signed assignment.
// Policy and zone authorization are checked before any Service API mutation.
func (c *Control) provisionApplication(w http.ResponseWriter, r *http.Request) {
	node, err := c.callerNode(r, "docker-gateway")
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	var input struct {
		Hostname    string `json:"hostname"`
		BackendPort uint16 `json:"backend_port"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.BackendPort == 0 {
		http.Error(w, "hostname and valid backend_port are required", 400)
		return
	}
	input.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(input.Hostname), "."))
	if err := policy.ValidateHostname(input.Hostname); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var settings struct {
		Name         string `json:"name"`
		LoadBalancer string `json:"loadbalancer_hostname"`
	}
	if err := c.Store.GetJSON("tailnet", &settings); err != nil {
		http.Error(w, "Unable to read control settings", 503)
		return
	}
	if !c.anyEdgeAllows(c.loadExposurePolicy(), input.Hostname, []string{node.ID, node.TailnetID}, principal(r).RoleSelectors()) {
		http.Error(w, "hostname denied by exposure policy", 403)
		return
	}
	if _, err := c.managedZone(input.Hostname); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	for _, app := range apps {
		for _, host := range app.PublicHostnames {
			if strings.EqualFold(strings.TrimSuffix(host, "."), input.Hostname) {
				if app.BackendSource == "tailnet" || !applicationAllows(app, node) || app.BackendPort != input.BackendPort {
					http.Error(w, "hostname already assigned to a different gateway or backend port", 409)
					return
				}
				c.nodeConfig(w, r)
				return
			}
		}
	}
	id := fmt.Sprintf("auto-%x", sha256.Sum256([]byte(node.ID+":"+input.Hostname+fmt.Sprintf(":%d", input.BackendPort))))[:25]
	app := store.ApplicationRecord{ID: id, ServiceName: "svc:tailscale-private-funnel-app-" + id, ServicePort: input.BackendPort, BackendPort: input.BackendPort, PublicHostnames: []string{input.Hostname}, AllowedGatewayNodes: []string{node.ID}, Enabled: true}
	clientID, err := c.Store.GetSecret("tailscale_oauth_client_id")
	if err != nil {
		http.Error(w, "configure Tailscale OAuth first", 428)
		return
	}
	secret, err := c.Store.GetSecret("tailscale_oauth_client_secret")
	if err != nil || settings.Name == "" {
		http.Error(w, "configure Tailscale OAuth and tailnet first", 428)
		return
	}
	service, err := c.tsClient(clientID, secret, settings.Name).CreateService(r.Context(), tailscale.Service{Name: app.ServiceName, DisplayName: input.Hostname, Ports: []string{fmt.Sprintf("tcp:%d", app.ServicePort)}, Tags: []string{"tag:tailscale-private-funnel"}})
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	app.ServiceFQDN = selectServiceTarget(service.Addresses, app.ServiceName, settings.Name)
	if err := c.Store.UpsertApplication(app); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("application.autoprovision:"+input.Hostname, "node:"+node.TailnetID)
	c.nodeConfig(w, r)
}

func (c *Control) callerGateway(r *http.Request) (store.NodeRecord, error) {
	n, err := c.callerNode(r, "")
	if err != nil {
		return n, err
	}
	if !store.IsGateway(n.Mode) {
		return n, fmt.Errorf("enrolled gateway required")
	}
	return n, nil
}
