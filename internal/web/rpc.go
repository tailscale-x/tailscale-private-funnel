package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	funnelv1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/rpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// rpcInterceptor derives authorization from the connection and the method's
// protobuf options. No permission or identity supplied in metadata is trusted.
func (c *Control) rpcInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			p, _ := ctx.Value(principalKey{}).(Principal)
			if p.NodeID == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("verified Tailscale identity required"))
			}
			method, ok := req.Spec().Schema.(protoreflect.MethodDescriptor)
			if !ok {
				return nil, connect.NewError(connect.CodeInternal, errors.New("RPC descriptor unavailable"))
			}
			permission, _ := proto.GetExtension(method.Options(), funnelv1.E_Permission).(funnelv1.Permission)
			if permission == funnelv1.Permission_PERMISSION_UNSPECIFIED {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("RPC permission is not configured"))
			}
			if permission == funnelv1.Permission_PERMISSION_VIEWER || permission == funnelv1.Permission_PERMISSION_OPERATOR || permission == funnelv1.Permission_PERMISSION_ADMIN {
				p.TailnetRole = c.tailnetRole(ctx, p)
				settings, err := c.loadAccessSettings()
				if err != nil {
					return nil, connect.NewError(connect.CodeUnavailable, errors.New("authorization settings unavailable"))
				}
				a := settings.permissions(p)
				allowed := a.Viewer
				if permission == funnelv1.Permission_PERMISSION_OPERATOR {
					allowed = a.Operator
				}
				if permission == funnelv1.Permission_PERMISSION_ADMIN {
					allowed = a.Admin
				}
				if !allowed {
					return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("required control permission: %s", permission))
				}
				ctx = context.WithValue(ctx, accessKey{}, a)
			}
			if permission == funnelv1.Permission_PERMISSION_NODE && req.Spec().Procedure != funnelv1connect.NodeServiceRegisterComponentProcedure {
				ctx = context.WithValue(ctx, componentContext{}, componentContext{ID: req.Header().Get("Funnel-Component-ID"), Instance: req.Header().Get("Funnel-Instance-ID")})
				var err error
				_, err = c.callerNodeContext(ctx, "")
				if err != nil {
					return nil, rpcFailure(connect.CodePermissionDenied, err)
				}
			}
			ctx = context.WithValue(ctx, principalKey{}, p)
			actor := p.Login
			if p.Tagged || actor == "" {
				actor = "node:" + p.NodeID
			}
			ctx = context.WithValue(ctx, identityContextKey{}, actor)
			if msg, ok := req.Any().(proto.Message); ok {
				if err := protovalidate.Validate(msg); err != nil {
					validation := connect.NewError(connect.CodeInvalidArgument, err)
					var violations *protovalidate.ValidationError
					if errors.As(err, &violations) {
						if detail, e := connect.NewErrorDetail(violations.ToProto()); e == nil {
							validation.AddDetail(detail)
						}
					}
					return nil, validation
				}
			}
			sensitive, _ := proto.GetExtension(method.Options(), funnelv1.E_Sensitive).(bool)
			response, err := next(ctx, req)
			if response != nil && sensitive {
				response.Header().Set("Cache-Control", "no-store")
			}
			audit, _ := proto.GetExtension(method.Options(), funnelv1.E_Audit).(bool)
			if audit {
				outcome := "ok"
				if err != nil {
					outcome = "failed"
				}
				_ = c.Store.Audit("rpc:"+string(method.FullName())+":"+outcome, actor)
			}
			return response, err
		}
	}
}
func (c *Control) registerRPC(r interface{ Handle(string, http.Handler) }) {
	path, handler := funnelv1connect.NewManagementServiceHandler(&managementRPC{c: c}, connect.WithInterceptors(c.rpcInterceptor()), connect.WithCodec(rpc.StrictJSON{}), connect.WithReadMaxBytes(2<<20))
	r.Handle(path+"*", handler)
	path, handler = funnelv1connect.NewDeploymentServiceHandler(&deploymentRPC{c: c}, connect.WithInterceptors(c.rpcInterceptor()), connect.WithCodec(rpc.StrictJSON{}), connect.WithReadMaxBytes(2<<20))
	r.Handle(path+"*", handler)
	path, handler = funnelv1connect.NewNodeServiceHandler(&nodeRPC{c: c}, connect.WithInterceptors(c.rpcInterceptor()), connect.WithCodec(rpc.StrictJSON{}), connect.WithReadMaxBytes(2<<20))
	r.Handle(path+"*", handler)

}
func rpcFailure(code connect.Code, err error) error {
	if err == nil {
		return nil
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce
	}
	return connect.NewError(code, err)
}
func rpcPrincipal(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}
func rpcActor(ctx context.Context) string { a, _ := ctx.Value(identityContextKey{}).(string); return a }
func isRPCPath(path string) bool          { return strings.HasPrefix(path, "/funnel.v1.") }

func connectCodeHTTP(status int) connect.Code {
	switch status {
	case 400:
		return connect.CodeInvalidArgument
	case 401:
		return connect.CodeUnauthenticated
	case 403:
		return connect.CodePermissionDenied
	case 404:
		return connect.CodeNotFound
	case 409:
		return connect.CodeAborted
	case 428:
		return connect.CodeFailedPrecondition
	case 429:
		return connect.CodeResourceExhausted
	case 502, 503:
		return connect.CodeUnavailable
	default:
		return connect.CodeInternal
	}
}
