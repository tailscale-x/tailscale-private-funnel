package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

// Session is shared by discovery implementations. A single node owns all
// listeners and reports, irrespective of the backend discovery mechanism.
type Session struct {
	HealthExtras map[string]any
	Resolver     HTTPResolver
	Listeners    *ListenerManager
	Agent        report.Agent
}

func (s Session) Publish(ctx context.Context, config nodeconfig.Configuration, runtimes []Runtime, extraServices []string) error {
	active := map[string]struct{}{}
	names := append([]string{}, extraServices...)
	exposures := make([]report.Exposure, 0, len(runtimes))
	reachable := 0
	for _, r := range runtimes {
		if r.Reachable {
			active[fmt.Sprintf("%s:%d", r.Assignment.ServiceName, r.Assignment.ServicePort)] = struct{}{}
			names = append(names, r.Assignment.ServiceName)
			reachable++
		}
		networks := []string{}
		for _, n := range r.Networks {
			networks = append(networks, n.ID)
		}
		exposures = append(exposures, report.Exposure{ApplicationID: r.Assignment.ApplicationID, ServiceName: r.Assignment.ServiceName, ContainerID: r.ContainerID, PublicHostname: r.Spec.Hostname, BackendHostname: r.BackendHostname, BackendIP: r.BackendIP, BackendDeviceID: r.BackendDeviceID, Port: r.Spec.Port, Protocol: "http", Networks: networks, Reachable: r.Reachable})
	}
	// Discovery can own several backend listeners per reported HTTP application.
	for _, a := range config.Applications {
		for _, b := range a.HTTPBackends {
			for _, name := range extraServices {
				if b.ServiceName == name {
					active[fmt.Sprintf("%s:%d", name, b.Port)] = struct{}{}
				}
			}
		}
	}
	s.Listeners.Reconcile(active)
	if err := s.Agent.Send(ctx, exposures); err != nil {
		return err
	}
	status, _ := json.Marshal(map[string]any{"last_report": time.Now().Unix(), "revision": config.Revision, "listeners": names, "backends": len(runtimes), "reachable": reachable})
	_ = os.WriteFile(filepath.Join(s.Agent.DataDir, "gateway-status.json"), status, 0600)
	if err := s.Resolver.Acknowledge(ctx, config.Revision, names); err != nil {
		return err
	}
	health := map[string]any{"backends": len(runtimes), "reachable": reachable, "at": time.Now().Unix()}
	for k, v := range s.HealthExtras {
		health[k] = v
	}
	if err := s.Resolver.ReportHealth(ctx, health); err != nil {
		slog.Warn("gateway health report failed", "error", err)
	}
	return nil
}
