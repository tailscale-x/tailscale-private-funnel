// Package workload bootstraps disposable identities using Kubernetes credentials.
package workload

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
)

func Issue(ctx context.Context, url, tokenFile, caFile string) (*v1.WorkloadBootstrap, error) {
	b, e := os.ReadFile(caFile)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		return nil, errors.New("invalid operator bootstrap CA")
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13}}}
	token, e := os.ReadFile(tokenFile)
	if e != nil {
		return nil, e
	}
	req := connect.NewRequest(&v1.Empty{})
	req.Header().Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	r, e := funnelv1connect.NewWorkloadBootstrapServiceClient(client, url).IssueKey(ctx, req)
	if e != nil {
		return nil, e
	}
	if r.Msg.AuthKey == "" || r.Msg.ComponentId == "" || r.Msg.InstanceId == "" || r.Msg.ExpiresAt <= time.Now().Unix() {
		return nil, errors.New("incomplete or expired workload bootstrap")
	}
	return r.Msg, nil
}
