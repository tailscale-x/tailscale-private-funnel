package web

import (
	"crypto/sha256"
	"net/http"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func (c *Control) tsClient(id, secret, tailnet string) tailscale.Client {
	if c.TailscaleClient != nil {
		return *c.TailscaleClient
	}
	key := sha256.Sum256([]byte(id + "\x00" + secret + "\x00" + tailnet))
	c.tokenClientsMu.Lock()
	defer c.tokenClientsMu.Unlock()
	if c.tokenClients == nil {
		c.tokenClients = map[[32]byte]tailscale.Client{}
	}
	if cached, ok := c.tokenClients[key]; ok {
		return cached
	}
	client := (tailscale.Client{ClientID: id, ClientSecret: secret, Tailnet: tailnet, HTTP: &http.Client{Timeout: 20 * time.Second}}).CachedOAuth()
	c.tokenClients[key] = client
	return client
}
