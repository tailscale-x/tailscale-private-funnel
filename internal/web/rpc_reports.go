package web

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func (s *managementRPC) ListReports(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Reports], error) {
	snapshots, err := s.c.Store.Reports()
	if err != nil {
		return nil, rpcFailure(connect.CodeInternal, err)
	}
	ids := make([]string, 0, len(snapshots))
	for id := range snapshots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ttl := s.c.ReportTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	out := &v1.Reports{}
	for _, id := range ids {
		node, err := s.c.Store.Node(id)
		if err != nil {
			return nil, rpcFailure(connect.CodeInternal, err)
		}
		var envelope report.Envelope
		if err = json.Unmarshal([]byte(snapshots[id]), &envelope); err != nil {
			return nil, rpcFailure(connect.CodeInternal, err)
		}
		var payload v1.ExposureReport
		if err = json.Unmarshal(envelope.Payload, &payload); err != nil {
			return nil, rpcFailure(connect.CodeInternal, err)
		}
		out.Items = append(out.Items, &v1.NodeReport{NodeId: id, Name: node.Name, Mode: node.Mode, ReceivedAt: node.LastReport, ExpiresAt: payload.ExpiresAt, Fresh: !node.Revoked && node.LastReport > time.Now().Add(-ttl).Unix() && payload.ExpiresAt > time.Now().Unix(), Exposures: payload.Exposures})
	}
	return connect.NewResponse(out), nil
}
func (s *managementRPC) ListServiceHosts(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.ServiceHosts], error) {
	hosts, err := s.c.Store.ServiceHosts()
	if err != nil {
		return nil, rpcFailure(connect.CodeInternal, err)
	}
	out := &v1.ServiceHosts{}
	for _, host := range hosts {
		out.Items = append(out.Items, &v1.ServiceHost{ServiceName: host.ServiceName, DeviceId: host.DeviceID, Approved: host.Approved, ApprovalState: host.ApprovalState, UpdatedAt: host.UpdatedAt})
	}
	return connect.NewResponse(out), nil
}
