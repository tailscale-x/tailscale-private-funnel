package kubebroker

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/rpc"
	"google.golang.org/protobuf/proto"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

type brokerCallerKey struct{}
type brokerRPC struct{ b *Broker }

func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	health := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" && b.Ready != nil && !b.Ready() {
			http.Error(w, "controller joining or stopping", 503)
			return
		}
		w.WriteHeader(200)
	}
	mux.HandleFunc("GET /readyz", health)
	mux.HandleFunc("GET /healthz", health)
	interceptor := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			if b.Ready != nil && !b.Ready() {
				return nil, connect.NewError(connect.CodeUnavailable, errors.New("controller joining or stopping"))
			}
			ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
			defer cancel()
			c, e := b.authenticate(ctx, r.Header().Get("Authorization"), r.Spec().Procedure)
			if e != nil {
				return nil, connect.NewError(connect.CodePermissionDenied, errors.New("forwarder authentication failed"))
			}
			if m, ok := r.Any().(proto.Message); ok {
				if e = protovalidate.Validate(m); e != nil {
					return nil, connect.NewError(connect.CodeInvalidArgument, e)
				}
			}
			if b.Allowed != nil {
				var state core.Secret
				e = b.Kube.Get(ctx, types.NamespacedName{Namespace: b.Namespace, Name: c.key}, &state)
				if e == nil && len(state.Data["node-id"]) > 0 {
					allowed, e := b.Allowed(ctx, string(state.Data["node-id"]))
					if e != nil {
						return nil, connect.NewError(connect.CodeUnavailable, errors.New("control authorization unavailable"))
					}
					if !allowed {
						state.Data = map[string][]byte{"revoked": []byte("true"), "node-id": state.Data["node-id"], "node-uid": state.Data["node-uid"]}
						_ = b.Kube.Update(ctx, &state)
						return nil, connect.NewError(connect.CodePermissionDenied, errors.New("forwarder revoked"))
					}
				} else if e != nil && (!apierrors.IsNotFound(e) || r.Spec().Procedure != funnelv1connect.BrokerServiceOpenSessionProcedure) {
					return nil, connect.NewError(connect.CodeUnavailable, errors.New("state unavailable"))
				}
			}
			if r.Spec().Procedure != funnelv1connect.BrokerServiceOpenSessionProcedure {
				if e = b.lease(ctx, c, false); e != nil {
					return nil, connect.NewError(connect.CodeAborted, e)
				}
			}
			response, e := next(context.WithValue(ctx, brokerCallerKey{}, c), r)
			if response != nil {
				response.Header().Set("Cache-Control", "no-store")
			}
			return response, e
		}
	})
	path, handler := funnelv1connect.NewBrokerServiceHandler(&brokerRPC{b: b}, connect.WithInterceptors(interceptor), connect.WithCodec(rpc.StrictJSON{}), connect.WithReadMaxBytes(2<<20))
	mux.Handle(path, handler)
	return mux
}
func callerFrom(ctx context.Context) caller { c, _ := ctx.Value(brokerCallerKey{}).(caller); return c }
func (s *brokerRPC) OpenSession(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.BrokerSession], error) {
	out, e := s.b.session(ctx, callerFrom(ctx))
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(out), nil
}
func (s *brokerRPC) GetConfig(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.SignedEnvelope], error) {
	if s.b.Configuration == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("configuration unavailable"))
	}
	env, e := s.b.Configuration(ctx)
	if e != nil {
		return nil, connect.NewError(connect.CodeUnavailable, e)
	}
	wire, e := report.Wire(env)
	if e != nil {
		return nil, connect.NewError(connect.CodeInternal, e)
	}
	return connect.NewResponse(wire), nil
}
func (s *brokerRPC) RenewLease(context.Context, *connect.Request[v1.Empty]) (*connect.Response[v1.Empty], error) {
	return connect.NewResponse(&v1.Empty{}), nil
}
func (s *brokerRPC) WriteState(ctx context.Context, r *connect.Request[v1.StateUpdate]) (*connect.Response[v1.Empty], error) {
	c := callerFrom(ctx)
	if !strings.HasPrefix(r.Msg.Key, "tsnet.") || len(r.Msg.Key) > 512 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid state key"))
	}
	e := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var state core.Secret
		if e := s.b.Kube.Get(ctx, types.NamespacedName{Namespace: s.b.Namespace, Name: c.key}, &state); e != nil {
			return e
		}
		if state.Data == nil {
			state.Data = map[string][]byte{}
		}
		state.Data[r.Msg.Key] = r.Msg.Value
		return s.b.Kube.Update(ctx, &state)
	})
	if e != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("state persistence failed"))
	}
	return connect.NewResponse(&v1.Empty{}), nil
}
func (s *brokerRPC) ReleaseLease(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.Empty], error) {
	c := callerFrom(ctx)
	var l coordination.Lease
	e := s.b.Kube.Get(ctx, types.NamespacedName{Namespace: s.b.Namespace, Name: c.key}, &l)
	if e == nil {
		zero := ""
		l.Spec.HolderIdentity = &zero
		l.Spec.RenewTime = nil
		e = s.b.Kube.Update(ctx, &l)
	}
	if e != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("lease release failed"))
	}
	return connect.NewResponse(&v1.Empty{}), nil
}
