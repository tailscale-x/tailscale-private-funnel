package web

import (
	"net/http"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func (c *Control) tsClient(id, secret, tailnet string) tailscale.Client {
	if c.TailscaleClient != nil {
		return *c.TailscaleClient
	}
	return tailscale.Client{ClientID: id, ClientSecret: secret, Tailnet: tailnet, HTTP: &http.Client{Timeout: 20 * time.Second}}
}
