package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	ts "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func (c *Control) tailscalePurposeClient(purpose string) (ts.Client, error) {
	var settings struct {
		Name string `json:"name"`
	}
	if e := c.Store.GetJSON("tailnet", &settings); e != nil {
		return ts.Client{}, e
	}
	id, e := c.Store.GetSecret("tailscale_" + purpose + "_client_id")
	if e != nil {
		return ts.Client{}, e
	}
	secret, e := c.Store.GetSecret("tailscale_" + purpose + "_client_secret")
	if e != nil {
		return ts.Client{}, e
	}
	return c.tsClient(id, secret, settings.Name), nil
}
func (s *managementRPC) oauth(ctx context.Context, in *v1.OAuthRequest, save bool) (*connect.Response[v1.TestResult], error) {
	s.c.settingsMu.Lock()
	defer s.c.settingsMu.Unlock()
	purpose := in.Purpose
	switch purpose {
	case "services", "users", "endpoints", "issuer":
	default:
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("credential purpose must be services, users, endpoints, or issuer"))
	}
	prefix := "tailscale_" + purpose
	id, key := strings.TrimSpace(in.ClientId), strings.TrimSpace(in.ClientSecret)
	if id == "" {
		id, _ = s.c.Store.GetSecret(prefix + "_client_id")
	}
	if key == "" {
		key, _ = s.c.Store.GetSecret(prefix + "_client_secret")
	}
	if id == "" || key == "" {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("client ID and secret required"))
	}
	var settings struct {
		Name string `json:"name"`
	}
	if e := s.c.Store.GetJSON("tailnet", &settings); e != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, e)
	}
	c := s.c.tsClient(id, key, settings.Name)
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var e error
	switch purpose {
	case "services":
		_, e = c.ListServices(bounded)
	case "users":
		_, e = c.ListUsers(bounded)
	case "endpoints":
		_, e = c.ListDevices(bounded)
	case "issuer":
		e = c.ValidateCredentials(bounded)
	}
	if e != nil {
		return nil, rpcFailure(connect.CodeUnavailable, errors.New("credential test failed; verify the selected purpose and its scoped permissions; stored credentials were not changed"))
	}
	message := "Credentials verified; no changes saved."
	if purpose == "issuer" {
		message = "Client authentication verified; auth_keys and delegated role-tag permissions are checked when a key is issued."
	}
	if save {
		if e = s.c.Store.SetSecrets(map[string]string{prefix + "_client_id": id, prefix + "_client_secret": key}); e != nil {
			return nil, rpcFailure(connect.CodeInternal, e)
		}
		s.c.roleMu.Lock()
		s.c.rolesExpire = time.Time{}
		s.c.roleUsers = nil
		s.c.roleMu.Unlock()
		message += " Saved."
	}
	return connect.NewResponse(&v1.TestResult{Ok: true, Message: message}), nil
}
func (s *managementRPC) RolePolicy(ctx context.Context, r *connect.Request[v1.RolePolicyRequest]) (*connect.Response[v1.RolePolicyTemplate], error) {
	port := r.Msg.ControlPort
	if port == 0 {
		port = 8080
	}
	if port > 65535 {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("invalid control TCP port"))
	}
	b, e := identity.PolicyTemplate(s.c.installation(), uint16(port))
	if e != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, e)
	}
	return connect.NewResponse(&v1.RolePolicyTemplate{Installation: s.c.installation(), PolicyJson: string(func() []byte { out, _ := json.MarshalIndent(b, "", "  "); return out }()), Warnings: []string{"Review and merge this policy in Tailscale; Funnel never applies it.", "Existing broad grants remain effective and can weaken installation isolation.", "Load balancers need ICMP-only access to this installation’s gateway devices, and runtime consumers need ICMP-only access to its control devices, for replacement Service-host discovery; TCP/UDP remains Service-only.", "Tailnet backend access requires an explicit administrator-managed grant."}}), nil
}
