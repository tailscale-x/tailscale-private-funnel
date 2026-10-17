package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

var ErrControlUnavailable = errors.New("control service unavailable")

type RPCResolver struct {
	BaseURL, NodeID, ControlKey string
	Client                      *http.Client
	Revision                    *string
}

func (r RPCResolver) ReportHealth(ctx context.Context, payload map[string]any) error {
	if r.Client == nil {
		return fmt.Errorf("%w: process-owned Tailscale HTTP client required", ErrControlUnavailable)
	}
	health, e := report.Payload(payload, &v1.NodeHealth{})
	if e != nil {
		return e
	}
	_, e = r.rpc().ReportHealth(ctx, connect.NewRequest(health))
	if e != nil {
		return fmt.Errorf("%w: %v", ErrControlUnavailable, e)
	}
	return nil
}
func (r RPCResolver) rpc() funnelv1connect.NodeServiceClient {
	return funnelv1connect.NewNodeServiceClient(r.Client, r.BaseURL)
}

func (r RPCResolver) Resolve(ctx context.Context, hostname string, backendPort uint16) (Assignment, error) {
	if strings.TrimSpace(r.BaseURL) == "" || strings.TrimSpace(r.NodeID) == "" || r.Client == nil {
		return Assignment{}, fmt.Errorf("%w: management URL and node ID are required", ErrControlUnavailable)
	}
	response, err := r.rpc().ResolveApplication(ctx, connect.NewRequest(&v1.ResolveApplicationRequest{Hostname: hostname, BackendPort: uint32(backendPort)}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnavailable || connect.CodeOf(err) == connect.CodeDeadlineExceeded {
			return Assignment{}, fmt.Errorf("%w: %v", ErrControlUnavailable, err)
		}
		return Assignment{}, err
	}
	envelope, err := report.FromWire(response.Msg, "configuration")
	if err != nil {
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
		if app.BackendSource == "tailnet" || !app.Enabled || (app.BackendPort != 0 && app.BackendPort != backendPort) || app.ServicePort == 0 {
			continue
		}
		matched := false
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

func (r RPCResolver) Acknowledge(ctx context.Context, revision string, services []string) error {
	if r.Client == nil {
		return fmt.Errorf("%w: process-owned Tailscale HTTP client required", ErrControlUnavailable)
	}
	if revision == "" {
		return fmt.Errorf("configuration revision required")
	}
	_, e := r.rpc().Acknowledge(ctx, connect.NewRequest(&v1.ConfigAck{Revision: revision, Services: services}))
	return e
}
func (r RPCResolver) Configuration(ctx context.Context) (nodeconfig.Configuration, error) {
	if r.Client == nil {
		return nodeconfig.Configuration{}, fmt.Errorf("%w: process-owned Tailscale HTTP client required", ErrControlUnavailable)
	}
	response, e := r.rpc().GetConfig(ctx, connect.NewRequest(&v1.Empty{}))
	if e != nil {
		return nodeconfig.Configuration{}, e
	}
	env, e := report.FromWire(response.Msg, "configuration")
	if e != nil {
		return nodeconfig.Configuration{}, e
	}
	return nodeconfig.Verify(env, r.ControlKey, r.NodeID, time.Now())
}
