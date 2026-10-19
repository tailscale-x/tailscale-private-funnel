package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func validatePersistentState(mode string, o Options) error {
	files := []string{}
	if mode == "control" {
		files = append(files, filepath.Join(o.DataDir, "master.key"))
	}
	if o.TailscaleConnection != "host" {
		files = append(files, filepath.Join(o.StateDir, "joined"), filepath.Join(o.StateDir, "tailscaled.state"))
	}
	if mode == "control" && o.Database.Type != "postgres" {
		files = append(files, filepath.Join(o.DataDir, "funnel.db"))
	}
	if mode != "control" {
		files = append(files, filepath.Join(o.DataDir, "node-id"), configPath(mode, o))
	}
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil || info.Size() == 0 {
			return fmt.Errorf("persistent %s state missing or empty: %s; restore storage rather than reenrolling", mode, f)
		}
	}
	if o.TailscaleConnection == "host" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(o.StateDir, "tailscaled.state"))
	if err != nil {
		return err
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(b, &state) != nil || len(state) == 0 {
		return fmt.Errorf("corrupt persistent Tailscale state")
	}
	return nil
}
func reportKubernetesEndpoint(ctx context.Context, s tsconnection.Connection, o Options) {
	c, _, err := kubernetesClients(o)
	if err != nil {
		slog.Error("Kubernetes Service discovery unavailable", "error", err)
		return
	}
	agent := report.Agent{NodeID: loadBalancerNodeID(o.DataDir), DataDir: o.DataDir, ManagementURL: o.ManagementURL, HTTPClient: tsnetHTTPClient(s)}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		var service core.Service
		err = c.Get(bounded, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.KubernetesService}, &service)
		if err == nil && service.Spec.Type == core.ServiceTypeLoadBalancer {
			payload := &v1.PublicEndpointPayload{Namespace: service.Namespace, Service: service.Name, Uid: string(service.UID), ObservedAt: time.Now().Unix()}
			for _, ip := range service.Status.LoadBalancer.Ingress {
				payload.Ingress = append(payload.Ingress, &v1.PublicIngress{Ip: ip.IP, Hostname: ip.Hostname})
			}
			err = agent.SendPublicEndpoint(bounded, payload)
		} else if err == nil {
			err = fmt.Errorf("public Service is not type LoadBalancer")
		}
		cancel()
		if err != nil {
			slog.Warn("Kubernetes public address discovery failed; retaining owned DNS", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
