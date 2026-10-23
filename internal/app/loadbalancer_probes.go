package app

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// Probe each backend, never an arbitrary hostname-level primary. Bounded parallel
// probes prevent one pending solver from serially delaying healthy applications.
func probeLoadBalancerBackends(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), apps []store.ApplicationRecord) []report.Exposure {
	type target struct {
		app           store.ApplicationRecord
		name, address string
		port          uint16
	}
	var targets []target
	for _, app := range apps {
		if !app.Enabled {
			continue
		}
		if app.BackendSource == "kubernetes" && len(app.HTTPRules) > 0 {
			for _, b := range app.HTTPBackends {
				targets = append(targets, target{app, b.ServiceName, b.Service, b.Port})
			}
			if len(app.HTTPBackends) == 0 {
				targets = append(targets, target{app, app.ServiceName, "", app.BackendPort})
			}
		} else {
			targets = append(targets, target{app, app.ServiceName, app.ServiceFQDN, app.ServicePort})
		}
	}
	results := make([][]report.Exposure, len(targets))
	var wg sync.WaitGroup
	jobs := make(chan int)
	for worker := 0; worker < min(8, len(targets)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					continue
				}
				t := targets[i]
				reachable := t.address == "" && len(t.app.HTTPRules) > 0 && len(t.app.HTTPBackends) == 0
				if !reachable {
					probe, cancel := context.WithTimeout(ctx, 5*time.Second)
					conn, err := dial(probe, "tcp", net.JoinHostPort(t.address, strconv.Itoa(int(t.port))))
					cancel()
					if conn != nil {
						_ = conn.Close()
					}
					reachable = err == nil
				}
				port := t.port
				if t.app.BackendSource != "kubernetes" {
					port = t.app.BackendPort
				}
				for _, host := range t.app.PublicHostnames {
					results[i] = append(results[i], report.Exposure{ApplicationID: t.app.ID, ServiceName: t.name, PublicHostname: host, BackendHostname: t.name, BackendIP: t.address, Port: port, Protocol: "http", Reachable: reachable})
				}
			}
		}()
	}
	for i := range targets {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	var out []report.Exposure
	for _, result := range results {
		out = append(out, result...)
	}
	return out
}
