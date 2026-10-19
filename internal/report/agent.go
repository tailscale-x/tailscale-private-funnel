package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"google.golang.org/protobuf/proto"
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
func (a Agent) envelope(purpose string, payload proto.Message) (*v1.SignedEnvelope, error) {
	if a.NodeID == "" || a.ManagementURL == "" || a.HTTPClient == nil {
		return nil, errors.New("report node and control URL required")
	}
	pub, key, e := LoadOrCreate(a.DataDir + "/report-identity.json")
	if e != nil {
		return nil, e
	}
	env, e := NewEnvelopeWithPurpose(purpose, a.NodeID, payload, pub, key, time.Now())
	if e != nil {
		return nil, e
	}
	return Wire(env)
}

// Payload converts a domain snapshot once into its generated wire model.
func Payload[T proto.Message](value any, out T) (T, error) {
	b, e := json.Marshal(value)
	if e == nil {
		e = json.Unmarshal(b, out)
	}
	return out, e
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
	env, e := a.envelope("report", &v1.ExposureReport{Exposures: items, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()})
	if e != nil {
		return e
	}
	_, e = a.client().SubmitReport(ctx, connect.NewRequest(env))
	if e == nil {
		a.RecordContact()
	}
	return e
}
func (a Agent) SendPeers(ctx context.Context, payload *v1.PeerSnapshot) error {
	env, e := a.envelope("peers", payload)
	if e != nil {
		return e
	}
	_, e = a.client().SubmitPeers(ctx, connect.NewRequest(env))
	return e
}
func (a Agent) SendKubernetes(ctx context.Context, payload *v1.KubernetesPayload) error {
	env, e := a.envelope("kubernetes", payload)
	if e != nil {
		return e
	}
	_, e = a.client().SubmitKubernetes(ctx, connect.NewRequest(env))
	if e == nil {
		a.RecordContact()
	}
	return e
}
func (a Agent) SendPublicEndpoint(ctx context.Context, payload *v1.PublicEndpointPayload) error {
	env, e := a.envelope("public-endpoint", payload)
	if e != nil {
		return e
	}
	_, e = a.client().SubmitPublicEndpoint(ctx, connect.NewRequest(env))
	return e
}
func (a Agent) BindForwarder(ctx context.Context) error {
	env, e := a.envelope("forwarder-binding", &v1.ForwarderBindingPayload{Bind: true})
	if e != nil {
		return e
	}
	_, e = a.client().BindForwarder(ctx, connect.NewRequest(env))
	return e
}
func (a Agent) EnrollForwarder(ctx context.Context, payload *v1.ForwarderEnrollmentPayload) (*v1.ForwarderEnrollment, error) {
	env, e := a.envelope("forwarder-enrollment", payload)
	if e != nil {
		return nil, e
	}
	result, e := a.client().EnrollForwarder(ctx, connect.NewRequest(env))
	if e != nil {
		return nil, fmt.Errorf("forwarder enrollment: %w", e)
	}
	return result.Msg, nil
}

// RecordContact persists no credentials and lets status inspection avoid opening tsnet.
func (a Agent) RecordContact() {
	if a.DataDir != "" {
		_ = os.WriteFile(filepath.Join(a.DataDir, "last-control-report"), []byte(fmt.Sprint(time.Now().Unix())), 0600)
	}
}

func (a Agent) SendDockerInventory(ctx context.Context, payload *v1.DockerInventoryPayload) error {
 env,e:=a.envelope("docker-inventory",payload); if e!=nil{return e}
 _,e=a.client().SubmitDockerInventory(ctx,connect.NewRequest(env));return e
}
