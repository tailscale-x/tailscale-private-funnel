package kubebroker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

type Client struct {
	rpc       funnelv1connect.BrokerServiceClient
	configRPC funnelv1connect.BrokerServiceClient
}

func NewClient(url, tokenFile, caFile string) (*Client, error) {
	b, e := os.ReadFile(caFile)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		return nil, errors.New("invalid broker CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}
	// Keep assignment acquisition bounded well inside the lease fencing margin.
	// Only configuration bootstrap uses the longer cold-fetch budget.
	httpClient := &http.Client{Timeout: 10 * time.Second, Transport: transport}
	configClient := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	token := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, r connect.AnyRequest) (connect.AnyResponse, error) {
			b, e := os.ReadFile(tokenFile)
			if e != nil {
				return nil, e
			}
			r.Header().Set("Authorization", "Bearer "+strings.TrimSpace(string(b)))
			return next(ctx, r)
		}
	})
	return &Client{rpc: funnelv1connect.NewBrokerServiceClient(httpClient, url, connect.WithInterceptors(token)), configRPC: funnelv1connect.NewBrokerServiceClient(configClient, url, connect.WithInterceptors(token))}, nil
}
func (c *Client) OpenSession(ctx context.Context) (*v1.BrokerSession, error) {
	r, e := c.rpc.OpenSession(ctx, connect.NewRequest(&v1.Empty{}))
	if e != nil {
		return nil, e
	}
	return r.Msg, nil
}
func (c *Client) GetConfig(ctx context.Context) (report.Snapshot, error) {
	r, e := c.configRPC.GetConfig(ctx, connect.NewRequest(&v1.Empty{}))
	if e != nil {
		return report.Snapshot{}, e
	}
	return report.NewSnapshot("configuration", r.Msg.NodeId, r.Msg, time.Unix(r.Msg.IssuedAt, 0))
}
func (c *Client) RenewLease(ctx context.Context) error {
	_, e := c.rpc.RenewLease(ctx, connect.NewRequest(&v1.Empty{}))
	return e
}

func (c *Client) ReleaseLease(ctx context.Context) error {
	_, e := c.rpc.ReleaseLease(ctx, connect.NewRequest(&v1.Empty{}))
	return e
}
