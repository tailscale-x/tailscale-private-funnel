package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func (c *Control) renameResource(w http.ResponseWriter, r *http.Request, kind string) {
	var in struct {
		Name string `json:"name"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		http.Error(w, "Only name is accepted", 400)
		return
	}
	name, err := store.ValidateName(in.Name)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id := chi.URLParam(r, "id")
	if err = c.Store.RenameResource(kind, id, name); err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	_ = c.Store.Audit(kind+".rename:"+id, identityFromRequest(r))
	writeJSON(w, map[string]string{"id": id, "name": name})
}
func (c *Control) renameNode(w http.ResponseWriter, r *http.Request) {
	c.renameResource(w, r, "node")
}
func (c *Control) renameEnrollment(w http.ResponseWriter, r *http.Request) {
	c.renameResource(w, r, "enrollment")
}
func renameForm(kind, id, name string) string {
	return fmt.Sprintf(`<form class="pf-inline-actions" data-rename="/api/%s/%s/name"><label class="pf-field"><span class="pf-label">Name</span><input class="pf-input" name="name" required maxlength="128" value="%s"></label><button class="pf-secondary-button">Save name</button><button class="pf-secondary-button" type="button" onclick="this.form.reset()">Cancel</button><span role="status"></span></form>`, safeText(kind), safeText(id), safeText(name))
}

const renameScript = `<script>document.querySelectorAll('[data-rename]').forEach(f=>f.onsubmit=async e=>{e.preventDefault();const s=f.querySelector('[role=status]');try{const r=await fetch(f.dataset.rename,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:f.elements.name.value})});if(!r.ok)throw Error(await r.text());s.textContent='Name saved.';location.reload();}catch(e){s.textContent=e.message;}});</script>`

// Resolve the edge strictly by internal ID. DNS names are operational values,
// never accepted as resource references or supplied by an gateway report.
func (c *Control) loadBalancerHostname(id string) (string, error) {
	n, err := c.Store.Node(id)
	if err != nil || n.Mode != "loadbalancer" || n.Revoked {
		return "", fmt.Errorf("active load balancer not found")
	}
	d, err := c.nodeDeployment(id)
	if err != nil {
		return "", err
	}
	if d.LoadBalancerHostname == "" {
		return "", fmt.Errorf("load balancer has no public DNS hostname")
	}
	return d.LoadBalancerHostname, nil
}

func (c *Control) providerDisplayName(id string) string {
	p, _, err := c.Store.ProviderConfig(id)
	if err != nil {
		return id
	}
	return p.Name + " · " + vendorName(p.Provider) + " · " + p.ID[:min(len(p.ID), 6)]
}

func (c *Control) nodeDisplayName(id string) string {
	n, err := c.Store.Node(id)
	if err != nil {
		return id
	}

	return n.Name + " · " + n.Hostname + " · " + id[:min(len(id), 6)]
}
