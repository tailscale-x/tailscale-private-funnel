package app

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/types/views"
	"testing"
	"time"
)

type registrationConnection struct {
	tsconnection.Connection
	client *http.Client
	role   identity.Role
}

func (c registrationConnection) HTTPClient() *http.Client { return c.client }
func (c registrationConnection) Up(context.Context) (*ipnstate.Status, error) {
	tags := views.SliceOf([]string{identity.Tag("main", c.role)})
	return &ipnstate.Status{Self: &ipnstate.PeerStatus{ID: "existing-device", Tags: &tags}}, nil
}
func TestRetainedEdgeStartsBeforeRegistrationAndHonorsDenial(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "retained"}[cached], func(t *testing.T) {
			release := make(chan struct{})
			started := make(chan struct{}, 1)
			handler := connect.NewUnaryHandler(funnelv1connect.NodeServiceRegisterComponentProcedure, func(ctx context.Context, r *connect.Request[v1.RegisterComponentRequest]) (*connect.Response[v1.RegisteredComponent], error) {
				select {
				case started <- struct{}{}:
				default:
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-release:
					return nil, connect.NewError(connect.CodePermissionDenied, errors.New("revoked"))
				}
			})
			srv := httptest.NewServer(handler)
			defer srv.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			o := Options{Installation: "main", DataDir: t.TempDir(), ManagementURL: srv.URL, Hostname: "edge"}
			if e := os.WriteFile(filepath.Join(o.DataDir, "node-id"), []byte("edge"), 0600); e != nil {
				t.Fatal(e)
			}
			if cached {
				if e := saveRouteCache(o, nil); e != nil {
					t.Fatal(e)
				}
			}
			failure := make(chan error, 1)
			done := make(chan error, 1)
			go func() {
				_, e := registerRuntimeWithCache(ctx, "loadbalancer", &o, registrationConnection{client: srv.Client(), role: identity.LoadBalancer}, failure)
				done <- e
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("registration did not start")
			}
			if cached {
				select {
				case e := <-done:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(time.Second):
					t.Fatal("cached edge blocked behind registration")
				}
			} else {
				select {
				case <-done:
					t.Fatal("fresh edge started without registration")
				case <-time.After(30 * time.Millisecond):
				}
			}
			close(release)
			if cached {
				select {
				case e := <-failure:
					if connect.CodeOf(e) != connect.CodePermissionDenied {
						t.Fatal(e)
					}
				case <-time.After(time.Second):
					t.Fatal("explicit denial did not stop retained startup")
				}
			} else {
				select {
				case e := <-done:
					if connect.CodeOf(e) != connect.CodePermissionDenied {
						t.Fatal(e)
					}
				case <-time.After(time.Second):
					t.Fatal("fresh registration did not finish")
				}
			}
		})
	}
}
