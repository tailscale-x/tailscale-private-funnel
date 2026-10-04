package ingress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrControlUnavailable = errors.New("control service unavailable")

type HTTPResolver struct {
	BaseURL, NodeID, ControlKey string
	Client                      *http.Client
	Revision                    *string
}

func (r HTTPResolver) ReportHealth(ctx context.Context, payload map[string]any) error {
	if strings.TrimSpace(r.BaseURL) == "" || strings.TrimSpace(r.NodeID) == "" {
		return fmt.Errorf("%w: management URL and node ID are required", ErrControlUnavailable)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.BaseURL, "/")+"/api/node/health", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrControlUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		if res.StatusCode >= 500 {
			return fmt.Errorf("%w: HTTP %d", ErrControlUnavailable, res.StatusCode)
		}
		return fmt.Errorf("health report: HTTP %d", res.StatusCode)
	}
	return nil
}

func (r HTTPResolver) Resolve(ctx context.Context, hostname string, backendPort uint16) (Assignment, error) {
	if strings.TrimSpace(r.BaseURL) == "" || strings.TrimSpace(r.NodeID) == "" {
		return Assignment{}, fmt.Errorf("%w: management URL and node ID are required", ErrControlUnavailable)
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.BaseURL, "/")+"/api/node/config", nil)
	if err != nil {
		return Assignment{}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return Assignment{}, fmt.Errorf("%w: %v", ErrControlUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode >= 500 {
			return Assignment{}, fmt.Errorf("%w: HTTP %d: %s", ErrControlUnavailable, res.StatusCode, strings.TrimSpace(string(b)))
		}
		return Assignment{}, fmt.Errorf("management config: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	var envelope report.Envelope
	if err := json.NewDecoder(res.Body).Decode(&envelope); err != nil {
		return Assignment{}, err
	}
	config, err := nodeconfig.Verify(envelope, r.ControlKey, r.NodeID, time.Now())
	if err != nil {
		return Assignment{}, fmt.Errorf("invalid signed control configuration: %w", err)
	}
	if r.Revision != nil {
		*r.Revision = config.Revision
	}
	for _, app := range config.Applications {
		if !app.Enabled || (app.BackendPort != 0 && app.BackendPort != backendPort) || app.ServicePort == 0 {
			continue
		}
		matched := strings.EqualFold(app.ID, hostname) || strings.EqualFold(app.ServiceName, hostname)
		for _, publicHostname := range app.PublicHostnames {
			if strings.EqualFold(strings.TrimSuffix(publicHostname, "."), strings.TrimSuffix(hostname, ".")) {
				matched = true
				break
			}
		}
		if matched {
			return Assignment{ApplicationID: app.ID, ServiceName: app.ServiceName, ServicePort: app.ServicePort, Enabled: true}, nil
		}
	}
	return Assignment{}, fmt.Errorf("hostname %q is not approved", hostname)
}

func (r HTTPResolver) Acknowledge(ctx context.Context, revision string, services []string) error {
	if strings.TrimSpace(r.BaseURL) == "" || strings.TrimSpace(r.NodeID) == "" || strings.TrimSpace(revision) == "" {
		return fmt.Errorf("control configuration acknowledgement requires node, URL, and revision")
	}
	body, _ := json.Marshal(map[string]any{"revision": revision, "services": services})
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.BaseURL, "/")+"/api/node/config/ack", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrControlUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("configuration acknowledgement: HTTP %d", res.StatusCode)
	}
	return nil
}
