package web

import (
	"encoding/base64"
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"net/http"
	"time"
)

func (c *Control) enrollForwarder(w http.ResponseWriter, r *http.Request) {
	parent, err := c.callerNode(r, "k8s-gateway")
	if err != nil {
		http.Error(w, "enrolled Kubernetes controller required", 403)
		return
	}
	var env report.Envelope
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&env) != nil || env.NodeID != parent.ID || env.PublicKey != parent.PublicKey {
		http.Error(w, "signed controller request required", 403)
		return
	}
	if _, _, err = report.Verify(env, time.Now(), time.Minute); err != nil {
		http.Error(w, "invalid controller signature", 401)
		return
	}
	var in struct {
		NodeUID   string `json:"node_uid"`
		Hostname  string `json:"hostname"`
		PublicKey string `json:"public_key"`
	}
	if json.Unmarshal(env.Payload, &in) != nil || in.NodeUID == "" || len(in.NodeUID) > 128 || len(in.Hostname) > 63 || in.Hostname == "" {
		http.Error(w, "invalid forwarder", 400)
		return
	}
	key, e := base64.RawStdEncoding.DecodeString(in.PublicKey)
	if e != nil || len(key) != 32 {
		http.Error(w, "invalid signing key", 400)
		return
	}
	if err = c.Store.AcceptNodeSnapshot(parent.ID, env.Nonce, env.PublicKey, "forwarder_enrollment:"+parent.ID, env.Payload); err != nil {
		http.Error(w, "replayed request", 409)
		return
	}
	n, err := c.Store.ReserveForwarder(parent.ID, in.NodeUID, in.Hostname, in.PublicKey)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if n.TailnetID != "" {
		http.Error(w, "registered identity already exists; restore its stored state", 409)
		return
	}
	ts, err := c.tailscaleRuntimeClient()
	if err != nil {
		http.Error(w, "Tailscale unavailable", 503)
		return
	}
	auth, err := ts.CreateAuthKey(r.Context(), "k8s-forwarder", in.Hostname, []string{"tag:tailscale-private-funnel"})
	if err != nil {
		http.Error(w, "auth key creation failed", 502)
		return
	}
	_ = c.Store.Audit("forwarder.enroll:"+n.ID, "node:"+parent.ID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]string{"node_id": n.ID, "auth_key": auth})
}
func (c *Control) bindForwarder(w http.ResponseWriter, r *http.Request) {
	var env report.Envelope
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&env) != nil {
		http.Error(w, "signed binding required", 400)
		return
	}
	n, e := c.Store.Node(env.NodeID)
	if e != nil || n.Mode != "k8s-forwarder" || n.Revoked || n.ParentID == "" || env.PublicKey != n.PublicKey || principal(r).NodeID == "" || !contains(principal(r).Tags, "tag:tailscale-private-funnel") {
		http.Error(w, "unknown forwarder", 403)
		return
	}
	parent, e := c.Store.Node(n.ParentID)
	if e != nil || parent.Revoked {
		http.Error(w, "controller revoked", 403)
		return
	}
	if _, _, e = report.Verify(env, time.Now(), time.Minute); e != nil {
		http.Error(w, "invalid binding signature", 401)
		return
	}
	if e = c.Store.BindNodeIdentity(n.ID, n.PublicKey, principal(r).NodeID, nil); e != nil {
		http.Error(w, "identity already bound", 409)
		return
	}
	w.WriteHeader(204)
}

func (c *Control) forwarderInventory(w http.ResponseWriter, r *http.Request) {
	parent, e := c.callerNode(r, "k8s-gateway")
	if e != nil {
		http.Error(w, "enrolled controller required", 403)
		return
	}
	children, e := c.Store.Forwarders(parent.ID)
	if e != nil {
		http.Error(w, "inventory unavailable", 503)
		return
	}
	writeJSON(w, children)
}
