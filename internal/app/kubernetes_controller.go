package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kube"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func kubernetesClients(o Options) (client.Client, kubernetes.Interface, error) {
	cfg, e := rest.InClusterConfig()
	if o.Kubeconfig != "" {
		cfg, e = clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	}
	if e != nil {
		return nil, nil, e
	}
	// Identity reads/writes must fail rather than hang indefinitely if the API
	// server is unavailable. These direct clients do not run resource watches.
	cfg.Timeout = 15 * time.Second
	c, e := client.New(cfg, client.Options{Scheme: kube.Scheme()})
	if e != nil {
		return nil, nil, e
	}
	k, e := kubernetes.NewForConfig(cfg)
	return c, k, e
}
func ServeKubernetesAgent(ctx context.Context, o Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, k, e := kubernetesClients(o)
	if e != nil {
		return e
	}
	if o.Installation == "" || o.KubeNamespace == "" {
		return fmt.Errorf("installation and namespace required")
	}
	o.StateDir = "/var/lib/tailscale-private-funnel/state"
	o.DataDir = "/var/lib/tailscale-private-funnel/data"
	if o.ComponentID == "" {
		o.ComponentID = os.Getenv("FUNNEL_COMPONENT_ID")
	}
	if o.ManagementURL == "" {
		o.ManagementURL = os.Getenv("FUNNEL_CONTROL_URL")
	}
	o.RoutingID = os.Getenv("FUNNEL_ROUTING_ID")
	saved := savedConfig{ManagementURL: o.ManagementURL}
	resource := os.Getenv("FUNNEL_RESOURCE_NAME")
	if resource == "" {
		return fmt.Errorf("Funnel resource name required")
	}
	cert, e := tls.LoadX509KeyPair("/etc/funnel-broker-tls/tls.crt", "/etc/funnel-broker-tls/tls.key")
	if e != nil {
		return e
	}
	broker := &kubebroker.Broker{Kube: c, Tokens: k, Namespace: o.KubeNamespace, Installation: resource, DaemonSet: resource + "-forwarder", ServiceAccount: resource + "-forwarder", Audience: "tailscale-private-funnel/" + resource, ManagementURL: saved.ManagementURL, Ready: func() bool { return ctx.Err() == nil }, SessionReady: func() bool { return controllerHTTP.Load() != nil }}
	broker.ControllerID = o.ComponentID
	configCache := &kubebroker.ConfigurationCache{NodeID: broker.ControllerID}
	configCache.Fetch = func(ctx context.Context) (report.Snapshot, error) {
		var envelope report.Snapshot
		value := controllerHTTP.Load()
		if value == nil {
			return envelope, fmt.Errorf("controller joining")
		}
		response, e := funnelv1connect.NewNodeServiceClient(value.(*http.Client), saved.ManagementURL).GetConfig(ctx, connect.NewRequest(&v1.Empty{}))
		if e != nil {
			return envelope, e
		}
		return report.NewSnapshot("configuration", response.Msg.NodeId, response.Msg, time.Unix(response.Msg.IssuedAt, 0))
	}
	broker.Configuration = configCache.Get
	// Broker requests use the singleton controller's Service endpoint.

	server := &http.Server{Addr: ":9443", Handler: broker.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		fresh, e := tls.LoadX509KeyPair("/etc/funnel-broker-tls/tls.crt", "/etc/funnel-broker-tls/tls.key")
		return &fresh, e
	}}}
	defer server.Close()
	// Each Pod uses a disposable embedded identity; broker leases coordinate assignments.
	serveDone := make(chan error, 1)
	go func() { serveDone <- Serve(ctx, "k8s-gateway", o) }()
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.ListenAndServeTLS("", "") }()
	var result error
	stopped := false
	select {
	case <-ctx.Done():
	case result = <-serveDone:
		stopped = true
	case result = <-httpDone:
		if result == http.ErrServerClosed {
			result = nil
		}
	}
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
	if !stopped {
		select {
		case err := <-serveDone:
			if result == nil {
				result = err
			}
		case <-shutdown.Done():
			if result == nil {
				result = fmt.Errorf("controller shutdown: %w", shutdown.Err())
			}
		}
	}
	return result
}

var controllerHTTP atomic.Value
