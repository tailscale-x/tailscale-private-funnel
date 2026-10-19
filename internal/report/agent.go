package report

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
)

// Exposure is a local discovery result, independent of the RPC representation.
type Exposure struct {
	PathPrefix      string               `json:"path_prefix,omitempty"`
	Rewrite         *proxy.PrefixRewrite `json:"rewrite,omitempty"`
	BackendDeviceID string               `json:"backend_device_id,omitempty"`
	ApplicationID   string               `json:"application_id,omitempty"`
	ServiceName     string               `json:"service_name,omitempty"`
	ContainerID     string               `json:"container_id,omitempty"`
	PublicHostname  string               `json:"public_hostname"`
	BackendHostname string               `json:"backend_hostname"`
	BackendIP       string               `json:"backend_ip"`
	Port            uint16               `json:"port"`
	Protocol        string               `json:"protocol"`
	Networks        []string             `json:"networks,omitempty"`
	Reachable       bool                 `json:"reachable"`
}
type Agent struct {
	NodeID, ManagementURL, DataDir string
	HTTPClient                     *http.Client
}

func (a Agent) client() funnelv1connect.NodeServiceClient {
	return funnelv1connect.NewNodeServiceClient(a.HTTPClient, a.ManagementURL)
}
func (a Agent) Send(ctx context.Context, exposures []Exposure) error {
	items := make([]*v1.ReportedExposure, 0, len(exposures))
	for _, e := range exposures {
		var rewrite *v1.PrefixRewrite
		if e.Rewrite != nil {
			rewrite = &v1.PrefixRewrite{ReplacePrefix: e.Rewrite.ReplacePrefix}
		}
		items = append(items, &v1.ReportedExposure{PathPrefix: e.PathPrefix, Rewrite: rewrite, BackendDeviceId: e.BackendDeviceID, ApplicationId: e.ApplicationID, ServiceName: e.ServiceName, ContainerId: e.ContainerID, PublicHostname: e.PublicHostname, BackendHostname: e.BackendHostname, BackendIp: e.BackendIP, Port: uint32(e.Port), Protocol: e.Protocol, Networks: e.Networks, Reachable: e.Reachable})
	}
	_, e := a.client().SubmitReport(ctx, connect.NewRequest(&v1.ExposureReport{Exposures: items, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}))
	if e == nil {
		a.RecordContact()
	}
	return e
}
func (a Agent) SendPeers(ctx context.Context, payload *v1.PeerSnapshot) error {
	_, e := a.client().SubmitPeers(ctx, connect.NewRequest(payload))
	if e == nil {
		a.RecordContact()
	}
	return e
}

func (a Agent) SendKubernetes(ctx context.Context, payload *v1.KubernetesPayload) error {
	_, e := a.client().SubmitKubernetes(ctx, connect.NewRequest(payload))
	if e == nil {
		a.RecordContact()
	}
	return e
}

func (a Agent) SendPublicEndpoint(ctx context.Context, payload *v1.PublicEndpointPayload) error {
	_, e := a.client().SubmitPublicEndpoint(ctx, connect.NewRequest(payload))
	if e == nil {
		a.RecordContact()
	}
	return e
}

func (a Agent) RecordContact() {
	if a.DataDir != "" {
		_ = os.WriteFile(filepath.Join(a.DataDir, "last-control-report"), []byte(fmt.Sprint(time.Now().Unix())), 0600)
	}
}

func (a Agent) SendDockerInventory(ctx context.Context, payload *v1.DockerInventoryPayload) error {
	_, e := a.client().SubmitDockerInventory(ctx, connect.NewRequest(payload))
	if e == nil {
		a.RecordContact()
	}
	return e
}
