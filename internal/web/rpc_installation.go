package web

import (
	"context"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

func (s *managementRPC) RenameApplication(ctx context.Context, r *connect.Request[v1.RenameRequest]) (*connect.Response[v1.Empty], error) {
	return connect.NewResponse(&v1.Empty{}), rpcFailure(connect.CodeInvalidArgument, s.c.Store.RenameResource("application", r.Msg.Id, r.Msg.Name))
}
