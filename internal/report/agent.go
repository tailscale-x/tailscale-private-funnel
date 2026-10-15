package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Exposure struct {
	BackendDeviceID string   `json:"backend_device_id,omitempty"`
	ApplicationID   string   `json:"application_id,omitempty"`
	ServiceName     string   `json:"service_name,omitempty"`
	ContainerID     string   `json:"container_id,omitempty"`
	PublicHostname  string   `json:"public_hostname"`
	BackendHostname string   `json:"backend_hostname"`
	BackendIP       string   `json:"backend_ip"`
	Port            uint16   `json:"port"`
	Protocol        string   `json:"protocol"`
	Networks        []string `json:"networks,omitempty"`
	Reachable       bool     `json:"reachable"`
}

type Agent struct {
	NodeID        string
	ManagementURL string
	DataDir       string
	HTTPClient    *http.Client
}

func (a Agent) Send(ctx context.Context, exposures []Exposure) error {
	if a.NodeID == "" || a.ManagementURL == "" {
		return fmt.Errorf("report node and management URL are required")
	}
	pub, priv, err := LoadOrCreate(a.DataDir + "/report-identity.json")
	if err != nil {
		return err
	}
	payload := map[string]any{"exposures": exposures, "expires_at": time.Now().Add(5 * time.Minute).Unix()}
	e, err := NewEnvelope(a.NodeID, payload, pub, priv, time.Now())
	if err != nil {
		return err
	}
	body, _ := json.Marshal(e)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.ManagementURL, "/")+"/api/reports", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := a.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		message, _ := io.ReadAll(res.Body)
		return fmt.Errorf("report submission failed: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

func (a Agent) SendSnapshot(ctx context.Context, path string, payload any) error {
	_, err := a.SnapshotResponse(ctx, path, payload)
	return err
}
func (a Agent) SnapshotResponse(ctx context.Context, path string, payload any) ([]byte, error) {
	pub, priv, err := LoadOrCreate(a.DataDir + "/report-identity.json")
	if err != nil {
		return nil, err
	}
	e, err := NewEnvelope(a.NodeID, payload, pub, priv, time.Now())
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(e)
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(a.ManagementURL, "/")+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("snapshot: HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 2<<20))
}
