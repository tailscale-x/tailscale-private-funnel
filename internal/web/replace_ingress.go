package web

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"net/http"
)

func (c *Control) replaceIngress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ReplacementID string `json:"replacement_id"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || in.ReplacementID == "" {
		http.Error(w, "replacement_id is required", 400)
		return
	}
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	oldID := chi.URLParam(r, "id")
	if err := c.Store.ReplaceIngress(oldID, in.ReplacementID); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	_ = c.Store.Audit("node.replace:"+oldID+":"+in.ReplacementID, identityFromRequest(r))
	w.WriteHeader(http.StatusNoContent)
}
