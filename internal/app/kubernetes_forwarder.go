package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"

	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"tailscale.com/tsnet"
)

func ServeKubernetesForwarder(ctx context.Context, o Options) error {
	client, e := kubebroker.NewClient(o.BrokerURL, o.BrokerTokenFile, o.BrokerCAFile)
	if e != nil {
		return e
	}
	var ready atomic.Bool
	var leaseActive atomic.Bool
	health := &http.Server{Addr: ":9091", ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" && (!ready.Load() || !leaseActive.Load()) {
			http.Error(w, "joining or local configuration unavailable", 503)
			return
		}
		w.WriteHeader(200)
	})}
	go func() { _ = health.ListenAndServe() }()
	defer health.Close()
	var state *v1.BrokerSession
	for {
		state, e = client.OpenSession(ctx)
		if e == nil {
			if state.ControllerId == "" || state.NodeId == "" || state.NodeUid == "" || state.NodeName == "" {
				return fmt.Errorf("broker returned an incomplete identity binding")
			}
			break
		}
		slog.Warn("forwarder waiting for broker", "error", e)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lease := newForwarderLease(client, state, &leaseActive, time.Now)
	go lease.run(ctx)
	data := "/var/lib/tailscale-private-funnel/data"
	dir := "/var/lib/tailscale-private-funnel/state"
	if e = os.MkdirAll(data, 0700); e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	o.DataDir = data
	o.StateDir = dir
	o.ComponentID = state.NodeId
	o.ParentID = state.ControllerId
	o.NodeUID = state.NodeUid
	o.NodeName = state.NodeName
	o.Hostname = state.NodeName
	key, e := prepareWorkload(ctx, "k8s-forwarder", &o)
	if e != nil {
		return e
	}
	native := &tsnet.Server{Dir: dir, Hostname: state.NodeName, AuthKey: key, Ephemeral: true}
	var s tsconnection.Connection = tsconnection.Wrap(native)
	defer s.Close()
	upCtx, upCancel := context.WithTimeout(ctx, 60*time.Second)
	defer upCancel()
	if _, e = s.Up(upCtx); e != nil {
		return e
	}
	native.AuthKey = ""
	key = ""
	s, e = registerRuntime(ctx, "k8s-forwarder", &o, s)
	if e != nil {
		return e
	}
	httpClient := tsnetHTTPClient(s)
	agent := report.Agent{NodeID: state.NodeId, ManagementURL: state.ManagementUrl, DataDir: data, HTTPClient: httpClient}
	listeners := gateway.NewListenerManager(s)
	lease.attach(listeners)
	defer func() {
		ready.Store(false)
		drainGatewayListeners(listeners)
		final, stop := context.WithTimeout(context.Background(), 5*time.Second)
		_ = agent.Send(final, nil)
		stop()
		// Identity ownership cannot transfer until the old tsnet stack is closed.
		_ = s.Close()
		release, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = client.ReleaseLease(release)
	}()

	resolver := gateway.RPCResolver{NodeID: state.NodeId, BaseURL: state.ManagementUrl, Client: httpClient}
	session := gateway.Session{Resolver: resolver, Listeners: listeners, Agent: agent}
	pools := map[string]*gateway.LocalPool{}
	continuity := map[string]*gateway.BackendContinuity{}
	poolStates := map[string]string{}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		if !leaseActive.Load() {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
				continue
			}
		}
		envelope, err := client.GetConfig(ctx)
		var config nodeconfig.Configuration
		if err == nil {
			config, err = nodeconfig.Read(envelope, state.ControllerId, time.Now())
		}
		if err == nil {
			runtimes := []gateway.Runtime{}
			services := []string{}
			endpointCount := 0
			counted := map[string]bool{}
			advertiseError := false
			unreachableServices := []string{}
			for _, app := range config.Applications {
				if app.BackendSource != "kubernetes" {
					continue
				}
				any := false
				for _, backend := range app.HTTPBackends {
					pool := pools[backend.Key]
					if pool == nil {
						pool = &gateway.LocalPool{AllowRemote: true}
						pools[backend.Key] = pool
						continuity[backend.Key] = &gateway.BackendContinuity{}
					}
					pool.Update(backend.Endpoints, state.NodeUid, state.NodeName)
					if !counted[backend.Key] {
						endpointCount += pool.Count()
						counted[backend.Key] = true
					}
					// New hosts advertise only with a local backend. Existing hosts can
					// keep serving verified remote endpoints after a Pod move.
					// This avoids coupling Service-host lifetime to Pod placement.
					if pool.Count() == 0 && !listeners.Has(backend.ServiceName, backend.Port) {
						continue
					}
					probe, stop := context.WithTimeout(ctx, 3*time.Second)
					reachable := false
					if listeners.Has(backend.ServiceName, backend.Port) {
						reachable = pool.Ready(probe)
					} else {
						reachable = pool.ReadyLocal(probe)
					}
					stop()
					stateKey := fmt.Sprintf("%d/%d/%t", pool.Count(), pool.RemoteCount(), reachable)
					if poolStates[backend.Key] != stateKey {
						slog.Info("Service backend pool changed", "service", backend.ServiceName, "local_endpoints", pool.Count(), "remote_endpoints", pool.RemoteCount(), "reachable", reachable)
						poolStates[backend.Key] = stateKey
					}
					if !reachable {
						unreachableServices = append(unreachableServices, "Service "+backend.ServiceName+": no reachable approved backend")
						if continuity[backend.Key].Keep(listeners.Has(backend.ServiceName, backend.Port), false, time.Now()) {
							services = append(services, backend.ServiceName)
							continue
						}
						listeners.Remove(backend.ServiceName, backend.Port)
						continue
					}
					continuity[backend.Key].Keep(listeners.Has(backend.ServiceName, backend.Port), true, time.Now())
					if err = listeners.ListenDial(ctx, backend.ServiceName, backend.Port, backend.Key, pool.Dial); err != nil {
						advertiseError = true
						slog.Warn("Service listener unavailable", "service", backend.ServiceName, "error", err)
						continue
					}
					services = append(services, backend.ServiceName)
					any = true
				}
				for _, host := range app.PublicHostnames {
					runtimes = append(runtimes, gateway.Runtime{Spec: gateway.Spec{Hostname: host, Port: app.BackendPort, Protocol: "http"}, Assignment: gateway.Assignment{ApplicationID: app.ID, ServiceName: app.ServiceName, ServicePort: app.ServicePort, Enabled: true}, BackendHostname: "kubernetes", Reachable: any})
				}
			}
			for key := range pools {
				if !counted[key] {
					delete(pools, key)
					delete(continuity, key)
					delete(poolStates, key)
				}
			}
			unique := []string{}
			seen := map[string]bool{}
			for _, name := range services {
				if !seen[name] {
					seen[name] = true
					unique = append(unique, name)
				}
			}
			services = unique
			reason := ""
			if len(services) == 0 {
				reason = "No ready node-local backends"
				if endpointCount > 0 {
					reason = "Local backends failed reachability"
				}
				if advertiseError {
					reason = "Tailscale Service advertisement pending"
				}
				if len(config.Applications) == 0 {
					reason = "No authorized applications"
				}
			}
			session.HealthExtras = map[string]any{"node_uid": state.NodeUid, "node_name": state.NodeName, "local_endpoints": endpointCount, "errors": unreachableServices, "withdrawal_reason": reason, "advertised_services": services}
			publishCtx, stopPublish := context.WithTimeout(ctx, 30*time.Second)
			err = session.Publish(publishCtx, config, runtimes, services)
			stopPublish()
			// Local forwarding readiness does not depend on report delivery.
			// The authenticated configuration and independently renewed assignment lease bound it.
			ready.Store(true)
		}
		if err != nil {
			slog.Warn("forwarder reconciliation failed", "error", err)
			// Connectivity loss is not an authoritative configuration withdrawal.
			// Keep existing listeners; the independently renewed broker assignment
			// lease still fences competing Pods and closes them on ownership loss.

		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("forwarder stopped: %w", ctx.Err())
		case <-tick.C:
		}
	}
}
