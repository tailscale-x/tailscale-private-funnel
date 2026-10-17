package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/oauth2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	HTTP                            *http.Client
	ClientID, ClientSecret, Tailnet string
	APIBase                         string
	OAuthBase                       string
	oauthSource                     oauth2.TokenSource
}
type authKeyRequest struct {
	Capabilities  map[string]any `json:"capabilities"`
	Description   string         `json:"description"`
	ExpirySeconds int            `json:"expirySeconds"`
}
type authKeyResponse struct {
	Key string `json:"key"`
}

func (c Client) CreateAuthKey(ctx context.Context, mode, hostname string, tags []string) (string, error) {
	accessToken, err := c.oauthToken(ctx)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(authKeyRequest{
		Description:   "tailscale-private-funnel-" + mode + "-" + hostname,
		ExpirySeconds: 900,
		Capabilities: map[string]any{
			"devices": map[string]any{
				"create": map[string]any{
					"reusable": false, "ephemeral": false, "preauthorized": true, "tags": tags,
				},
			},
		},
	})
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/keys"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("Tailscale auth key: HTTP %d", res.StatusCode)
	}
	var key authKeyResponse
	if err := json.NewDecoder(res.Body).Decode(&key); err != nil {
		return "", err
	}
	if key.Key == "" {
		return "", fmt.Errorf("Tailscale returned an empty auth key")
	}
	return key.Key, nil
}

type Service struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName,omitempty"`
	Addresses   []string `json:"addrs,omitempty"`
	Ports       []string `json:"ports,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type ServiceHost struct {
	NodeID        string `json:"nodeId"`
	ApprovalLevel string `json:"approvalLevel"`
	Configured    string `json:"configured"`
}

type Device struct {
	ID                 string   `json:"id"`
	NodeID             string   `json:"nodeId"`
	Name               string   `json:"name"`
	Hostname           string   `json:"hostname"`
	Tags               []string `json:"tags"`
	Authorized         bool     `json:"authorized"`
	ClientConnectivity struct {
		Endpoints []string `json:"endpoints"`
	} `json:"clientConnectivity"`
	Addresses []string `json:"addresses"`
}

func (c Client) ListDevices(ctx context.Context) ([]Device, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/devices"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
		return nil, fmt.Errorf("Tailscale devices: HTTP %d", res.StatusCode)
	}
	var payload struct {
		Devices []Device `json:"devices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Devices, nil
}

func (c Client) DeleteDevice(ctx context.Context, deviceID string) error {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/device/" + url.PathEscape(deviceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("Tailscale device delete: HTTP %d", res.StatusCode)
	}
	return nil
}

func (c Client) GetDevice(ctx context.Context, deviceID string) (Device, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return Device{}, err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/device/" + url.PathEscape(deviceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Device{}, err
	}
	q := req.URL.Query()
	q.Set("fields", "all")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client().Do(req)
	if err != nil {
		return Device{}, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return Device{}, fmt.Errorf("Tailscale device: HTTP %d", res.StatusCode)
	}
	var device Device
	if err := json.NewDecoder(res.Body).Decode(&device); err != nil {
		return Device{}, err
	}
	return device, nil
}

func (c Client) CreateService(ctx context.Context, service Service) (Service, error) {
	if service.Name == "" || len(service.Ports) == 0 {
		return Service{}, fmt.Errorf("service name and ports are required")
	}
	token, err := c.oauthToken(ctx)
	if err != nil {
		return Service{}, err
	}
	// The Services API requires the existing two TailVIP addresses when an
	// existing Service is updated. Preserve them so re-approving an application
	// is idempotent after a control restart.
	if existing, getErr := c.GetService(ctx, service.Name); getErr == nil && len(existing.Addresses) == 2 {
		service.Addresses = existing.Addresses
	}
	body, _ := json.Marshal(service)
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/services/" + url.PathEscape(service.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return Service{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client().Do(req)
	if err != nil {
		return Service{}, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(res.Body).Decode(&detail)
		if detail.Message != "" {
			return Service{}, fmt.Errorf("Tailscale Service create: HTTP %d: %s", res.StatusCode, detail.Message)
		}
		return Service{}, fmt.Errorf("Tailscale Service create: HTTP %d", res.StatusCode)
	}
	var out Service
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return Service{}, err
	}
	return out, nil
}

func (c Client) GetService(ctx context.Context, name string) (Service, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return Service{}, err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/services/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Service{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.client().Do(req)
	if err != nil {
		return Service{}, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return Service{}, fmt.Errorf("Tailscale Service get: HTTP %d", res.StatusCode)
	}
	var service Service
	if err := json.NewDecoder(res.Body).Decode(&service); err != nil {
		return Service{}, err
	}
	return service, nil
}

func (c Client) ListServices(ctx context.Context) ([]Service, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/services"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
		return nil, fmt.Errorf("Tailscale Service list: HTTP %d", res.StatusCode)
	}
	var payload struct {
		Services []Service `json:"vipServices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Services, nil
}

func (c Client) ListServiceHosts(ctx context.Context, serviceName string) ([]ServiceHost, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/services/" + url.PathEscape(serviceName) + "/devices"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
		return nil, fmt.Errorf("Tailscale Service hosts: HTTP %d", res.StatusCode)
	}
	var payload struct {
		Hosts []ServiceHost `json:"hosts"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Hosts, nil
}

func (c Client) ApproveServiceDevice(ctx context.Context, serviceName, deviceID string, approved bool) (bool, error) {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return false, err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/services/" + url.PathEscape(serviceName) + "/device/" + url.PathEscape(deviceID) + "/approved"
	body, _ := json.Marshal(map[string]bool{"approved": approved})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.client().Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return false, fmt.Errorf("Tailscale Service approval: HTTP %d", res.StatusCode)
	}
	var out struct {
		Approved bool `json:"approved"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Approved, nil
}

func (c Client) retrieveOAuthToken(ctx context.Context) (*oauth2.Token, error) {
	tokenReq := url.Values{"client_id": {c.ClientID}, "client_secret": {c.ClientSecret}, "grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.oauthBase(), "/")+"/api/v2/oauth/token", strings.NewReader(tokenReq.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("Tailscale OAuth token: HTTP %d", res.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(res.Body).Decode(&token); err != nil {
		return nil, err
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("Tailscale OAuth returned an empty token")
	}
	expiry := time.Now()
	if token.ExpiresIn > 0 {
		expiry = expiry.Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return &oauth2.Token{AccessToken: token.AccessToken, TokenType: "Bearer", Expiry: expiry}, nil
}

func (c Client) apiBase() string {
	if c.APIBase != "" {
		return c.APIBase
	}
	return "https://api.tailscale.com/api/v2"
}
func (c Client) oauthBase() string {
	if c.OAuthBase != "" {
		return c.OAuthBase
	}
	return "https://api.tailscale.com"
}

func (c Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// DeleteService removes a control-owned Service after its advertisements have
// been withdrawn. A missing Service is already cleaned up.
func (c Client) DeleteService(ctx context.Context, name string) error {
	token, err := c.oauthToken(ctx)
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(c.apiBase(), "/") + "/tailnet/" + url.PathEscape(c.Tailnet) + "/services/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("Tailscale Service deletion: HTTP %d", response.StatusCode)
	}
	return nil
}
