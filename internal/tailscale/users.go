package tailscale

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type User struct {
	ID        string `json:"id"`
	LoginName string `json:"loginName"`
	Role      string `json:"role"`
	Type      string `json:"type"`
	Status    string `json:"status"`
}

func (c Client) ListUsers(ctx context.Context) ([]User, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.apiBase(), "/")+"/tailnet/"+url.PathEscape(c.Tailnet)+"/users", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Tailscale user roles: HTTP %d (requires users:read)", res.StatusCode)
	}
	var out struct {
		Users []User `json:"users"`
	}
	err = json.NewDecoder(res.Body).Decode(&out)
	return out.Users, err
}
