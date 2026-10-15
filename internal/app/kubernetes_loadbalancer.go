package app

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"log/slog"
	"os"
	"path/filepath"
	"tailscale.com/tsnet"
	"time"
)

func validatePersistentState(mode string, o Options) error {
	files := []string{filepath.Join(o.StateDir, "joined"), filepath.Join(o.StateDir, "tailscaled.state"), filepath.Join(o.DataDir, "funnel.db"), filepath.Join(o.DataDir, "master.key")}
	if mode != "control" {
		files = append(files, filepath.Join(o.DataDir, "node-id"), filepath.Join(o.DataDir, "report-identity.json"), configPath(mode, o))
	}
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil || info.Size() == 0 {
			return fmt.Errorf("persistent %s state missing or empty: %s; restore storage rather than reenrolling", mode, f)
		}
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
func reportKubernetesEndpoint(ctx context.Context, s *tsnet.Server, o Options) {
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
			err = agent.SendSnapshot(bounded, "/api/node/public-endpoint", map[string]any{"namespace": service.Namespace, "service": service.Name, "uid": string(service.UID), "observed_at": time.Now().Unix(), "ingress": service.Status.LoadBalancer.Ingress})
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
