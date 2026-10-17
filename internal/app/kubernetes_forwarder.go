package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

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
	health := &http.Server{Addr: ":9091", ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" && !ready.Load() {
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
			if state.ControllerId == "" || state.NodeId == "" || state.NodeUid == "" || state.NodeName == "" || state.ControlKey == "" {
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
	var lastRenew atomic.Int64
	lastRenew.Store(time.Now().UnixNano())
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastAttempt := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if time.Since(time.Unix(0, lastRenew.Load())) >= 40*time.Second {
					ready.Store(false)
					cancel()
					return
				}
				if time.Since(lastAttempt) < 15*time.Second {
					continue
				}
				lastAttempt = time.Now()
				renew, stop := context.WithTimeout(ctx, 5*time.Second)
				err := client.RenewLease(renew)
				stop()
				if err == nil {
					lastRenew.Store(time.Now().UnixNano())
				} else {
					slog.Warn("forwarder lease renewal failed", "error", err)
				}
			}
		}
	}()
	data := "/var/lib/tailscale-private-funnel/data"
	dir := "/var/lib/tailscale-private-funnel/state"
	if e = os.MkdirAll(data, 0700); e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(data, "report-identity.json"), state.Identity, 0600); e != nil {
		return e
	}
	s := &tsnet.Server{Dir: dir, Hostname: state.NodeName + "-" + state.NodeId, Store: &kubebroker.RemoteStore{Client: client, State: state.State}, AuthKey: state.AuthKey, Ephemeral: false}
	defer s.Close()
	upCtx, upCancel := context.WithTimeout(ctx, 60*time.Second)
	defer upCancel()
	if _, e = s.Up(upCtx); e != nil {
		return e
	}
	s.AuthKey = ""
	state.AuthKey = ""
	httpClient := tsnetHTTPClient(s)
	agent := report.Agent{NodeID: state.NodeId, ManagementURL: state.ManagementUrl, DataDir: data, HTTPClient: httpClient}
	if e = agent.BindForwarder(ctx); e != nil {
		return e
	}
	listeners := gateway.NewListenerManager(s)
	defer listeners.Close()
	defer func() {
		ready.Store(false)
		drain, stop := context.WithTimeout(context.Background(), 10*time.Second)
		listeners.CloseWait(drain)
		stop()
		final, stop := context.WithTimeout(context.Background(), 5*time.Second)
		_ = agent.Send(final, nil)
		stop()
		// Identity ownership cannot transfer until the old tsnet stack is closed.
		_ = s.Close()
		release, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = client.ReleaseLease(release)
	}()

	resolver := gateway.RPCResolver{NodeID: state.NodeId, BaseURL: state.ManagementUrl, ControlKey: state.ControlKey, Client: httpClient}
	session := gateway.Session{Resolver: resolver, Listeners: listeners, Agent: agent}
	pools := map[string]*gateway.LocalPool{}
	configExpires := time.Now()
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		envelope, err := client.GetConfig(ctx)
		var config nodeconfig.Configuration
		if err == nil {
			config, err = nodeconfig.Verify(envelope, state.ControlKey, state.ControllerId, time.Now())
		}
		if err == nil {
			configExpires = time.Unix(envelope.Timestamp, 0).Add(5 * time.Minute)
			runtimes := []gateway.Runtime{}
			services := []string{}
			endpointCount := 0
			counted := map[string]bool{}
			advertiseError := false
			for _, app := range config.Applications {
				if app.BackendSource != "kubernetes" {
					continue
				}
				any := false
				for _, backend := range app.HTTPBackends {
					pool := pools[backend.Key]
					if pool == nil {
						pool = &gateway.LocalPool{}
						pools[backend.Key] = pool
					}
					pool.Update(backend.Endpoints, state.NodeUid, state.NodeName)
					if !counted[backend.Key] {
						endpointCount += pool.Count()
						counted[backend.Key] = true
					}
					probe, stop := context.WithTimeout(ctx, 3*time.Second)
					reachable := pool.Ready(probe)
					stop()
					if !reachable {
						listeners.Remove(backend.ServiceName, backend.Port)
						continue
					}
					if err = listeners.ListenDial(ctx, backend.ServiceName, backend.Port, backend.Key, pool.Dial); err != nil {
						advertiseError = true
						slog.Warn("local Service listener unavailable", "service", backend.ServiceName, "error", err)
						continue
					}
					services = append(services, backend.ServiceName)
					any = true
				}
				for _, host := range app.PublicHostnames {
					runtimes = append(runtimes, gateway.Runtime{Spec: gateway.Spec{Hostname: host, Port: app.BackendPort, Protocol: "http"}, Assignment: gateway.Assignment{ApplicationID: app.ID, ServiceName: app.ServiceName, ServicePort: app.ServicePort, Enabled: true}, BackendHostname: "kubernetes", Reachable: any})
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
			session.HealthExtras = map[string]any{"node_uid": state.NodeUid, "node_name": state.NodeName, "local_endpoints": endpointCount, "withdrawal_reason": reason, "advertised_services": services}
			publishCtx, stopPublish := context.WithTimeout(ctx, 30*time.Second)
			err = session.Publish(publishCtx, config, runtimes, services)
			stopPublish()
			// Local forwarding readiness does not depend on report delivery.
			// The signed configuration and independently renewed owner lease bound it.
			ready.Store(true)
		}
		if err != nil {
			slog.Warn("forwarder reconciliation failed", "error", err)
			if !time.Now().Before(configExpires) {
				listeners.Close()
				ready.Store(false)
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("forwarder stopped or lease lost: %w", ctx.Err())
		case <-tick.C:
		}
	}
}
