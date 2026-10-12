package web

import (
	"context"
	"strings"
	"time"
)

// Resolve account roles only for a WhoIs-authenticated human user. Tagged
// service identities cannot inherit their enrolling user's management role.
func (c *Control) tailnetRole(ctx context.Context, p Principal) string {
	if p.Tagged || len(p.Tags) > 0 || p.UserID == "" || p.Login == "" {
		return ""
	}
	c.roleMu.Lock()
	defer c.roleMu.Unlock()
	if time.Now().After(c.rolesExpire) {
		c.roleUsers = nil
		var settings struct {
			Name string `json:"name"`
		}
		_ = c.Store.GetJSON("tailnet", &settings)
		id, e1 := c.Store.GetSecret("tailscale_oauth_client_id")
		secret, e2 := c.Store.GetSecret("tailscale_oauth_client_secret")
		if e1 != nil || e2 != nil || settings.Name == "" {
			return ""
		}
		users, err := c.tsClient(id, secret, settings.Name).ListUsers(ctx)
		if err != nil {
			c.rolesExpire = time.Now().Add(10 * time.Second)
			return ""
		}
		c.roleUsers = users
		c.rolesExpire = time.Now().Add(30 * time.Second)
	}
	// WhoIs uses a numeric UserID; the REST Users API uses an opaque stable ID.
	// Bind using the authenticated login within this API client's tailnet,
	// excluding shared users and suspended/unapproved accounts.
	for _, u := range c.roleUsers {
		if strings.EqualFold(u.LoginName, p.Login) && u.Type == "member" && (u.Status == "active" || u.Status == "idle") {
			return u.Role
		}
	}
	return ""
}
