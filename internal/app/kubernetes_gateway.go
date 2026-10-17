package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"

	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kube"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"tailscale.com/tsnet"
)

func runKubernetesGateway(ctx context.Context, o Options, s *tsnet.Server) error {
	path := filepath.Join(o.DataDir, "cluster-id")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		id, e := store.NewResourceID()
		if e != nil {
			return e
		}
		data = []byte(id)
		err = os.WriteFile(path, data, 0600)
	}
	if err != nil {
		return err
	}
	if o.GatewayClass == "" {
		o.GatewayClass = "tailscale-private-funnel"
	}
	if o.IngressClass == "" {
		o.IngressClass = "tailscale-private-funnel"
	}
	if o.ProbeListen == "" {
		o.ProbeListen = ":9090"
	}
	session := gateway.Session{Resolver: gateway.RPCResolver{BaseURL: o.ManagementURL, NodeID: loadBalancerNodeID(o.DataDir), ControlKey: o.ControlKey, Client: tsnetHTTPClient(s)}, Listeners: gateway.NewListenerManager(s), Agent: report.Agent{NodeID: loadBalancerNodeID(o.DataDir), ManagementURL: o.ManagementURL, DataDir: o.DataDir, HTTPClient: tsnetHTTPClient(s)}}
	defer session.Listeners.Close()
	return kube.Run(ctx, o.Kubeconfig, o.ProbeListen, kube.Options{GatewayClass: o.GatewayClass, IngressClass: o.IngressClass, Namespaces: o.WatchNamespaces, ControllerName: o.ControllerName, ClusterID: string(data)}, func(ctx context.Context, result kube.Result) (kube.Publication, error) {
		snapshot := nodeconfig.KubernetesSnapshot{ClusterID: string(data), Hosts: result.Hosts, Errors: result.Problems, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
		payload, convertErr := report.Payload(snapshot, &v1.KubernetesPayload{})
		if convertErr != nil {
			return kube.Publication{}, convertErr
		}
		if err := session.Agent.SendKubernetes(ctx, payload); err != nil {
			return kube.Publication{}, err
		}
		config, err := session.Resolver.Configuration(ctx)
		if err != nil {
			return kube.Publication{}, err
		}
		// Controllers publish routing intent only. Child forwarders own all listeners.
		if err = session.Resolver.ReportHealth(ctx, map[string]any{"online": true, "last_report": time.Now().Unix()}); err != nil {
			return kube.Publication{}, err
		}

		readyHosts := []string{}
		readyHosts = append(readyHosts, config.PublishedHostnames...)
		return kube.Publication{Hosts: readyHosts, Addresses: config.PublishedAddresses}, nil
	})
}
