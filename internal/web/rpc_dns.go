package web

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

func (s *managementRPC) ZoneDNS(ctx context.Context, r *connect.Request[v1.ResourceID]) (*connect.Response[v1.ZoneDNSState], error) {
	z, e := s.c.Store.ZoneByID(r.Msg.Id)
	if e != nil {
		return nil, rpcFailure(connect.CodeNotFound, e)
	}
	entries, e := s.c.Store.PartitionZoneLedger(z.ProviderID, z.Zone)
	if e != nil {
		return nil, rpcFailure(connect.CodeInternal, e)
	}
	out := &v1.ZoneDNSState{Domain: z.Zone}
	for _, entry := range entries {
		rr := entry.Record.RR()
		owner := strings.SplitN(entry.OwnerID, ":", 2)
		name := "Unknown node"
		if len(owner) == 2 {
			if owner[0] == "exposure" {
				if exposure, e := s.c.Store.Exposure(owner[1]); e == nil {
					name = exposure.Hostname
				}
			} else {
				name = s.c.nodeDisplayName(owner[1])
			}
		}
		management := "Managed by Local Funnel"
		if strings.HasPrefix(entry.OwnerID, "foreign-provider:") {
			management = "Managed by another node"
		}
		out.Records = append(out.Records, &v1.DNSRecord{Name: rr.Name, Type: rr.Type, Data: rr.Data, Node: name, Management: management})
	}
	_ = s.c.Store.GetJSON("zone_sync:"+z.ProviderID+":"+z.Zone, &out.Status)
	_ = s.c.Store.GetJSON("zone_last_sync:"+z.ID, &out.LastSuccess)
	return connect.NewResponse(out), nil
}
func (s *managementRPC) PreviewDNS(ctx context.Context, r *connect.Request[v1.DNSRequest]) (*connect.Response[v1.DNSResult], error) {
	if len(r.Msg.Approvals) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("preview does not accept overwrite approvals"))
	}
	return s.dns(ctx, r.Msg, true)
}
func (s *managementRPC) ApplyDNS(ctx context.Context, r *connect.Request[v1.DNSRequest]) (*connect.Response[v1.DNSResult], error) {
	return s.dns(ctx, r.Msg, false)
}
func (s *managementRPC) dns(ctx context.Context, r *v1.DNSRequest, dryRun bool) (*connect.Response[v1.DNSResult], error) {
	result, e := s.c.zoneDNSSyncOperation(ctx, r.ZoneId, dryRun, r.Approvals, rpcActor(ctx))
	if e != nil {
		return nil, e
	}
	out := &v1.DNSResult{DryRun: dryRun, Pending: result["pending"] == true}
	items, _ := result["results"].([]map[string]any)
	for _, item := range items {
		entry := &v1.DNSPartitionResult{ResourceId: stringValue(item, "node_id", ""), Name: stringValue(item, "node_name", ""), Error: stringValue(item, "error", ""), RequiresApproval: item["requires_approval"] == true, ApprovalToken: stringValue(item, "approval_token", "")}
		if n, ok := item["add"].(int); ok {
			entry.AddCount = uint32(n)
		}
		if n, ok := item["remove"].(int); ok {
			entry.RemoveCount = uint32(n)
		}
		for key, target := range map[string]*[]*v1.DNSRecord{"existing_records": &entry.ExistingRecords, "desired_records": &entry.DesiredRecords} {
			records, _ := item[key].([]map[string]any)
			for _, record := range records {
				*target = append(*target, &v1.DNSRecord{Name: stringValue(record, "name", ""), Type: stringValue(record, "type", ""), Data: stringValue(record, "data", ""), Management: stringValue(record, "management", "")})
			}
		}
		out.Results = append(out.Results, entry)
	}
	return connect.NewResponse(out), nil
}
