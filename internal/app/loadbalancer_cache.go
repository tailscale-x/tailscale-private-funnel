package app

import (
	"encoding/json"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"os"
	"path/filepath"
)

type routeCache struct {
	NodeID       string        `json:"node_id"`
	ControlURL   string        `json:"control_url"`
	Installation string        `json:"installation"`
	Routes       []proxy.Route `json:"routes"`
}

// Start from the last successfully applied set (including an authoritative
// empty set) before any control RPC. Refresh revalidates in the normal loop.
func initialLoadBalancerRoutes(o Options, fetch func() ([]proxy.Route, error)) ([]proxy.Route, error) {
	cached, cacheErr := loadRouteCache(o)
	if cacheErr == nil {
		return cached, nil
	}
	routes, err := fetch()
	if err != nil {
		return nil, fmt.Errorf("control configuration unavailable: %w (saved routes: %v)", err, cacheErr)
	}
	return routes, nil
}

func loadRouteCache(o Options) ([]proxy.Route, error) {
	b, err := os.ReadFile(filepath.Join(o.DataDir, "loadbalancer-routes.json"))
	if err != nil {
		return nil, err
	}
	var c routeCache
	if err = json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.NodeID != loadBalancerNodeID(o.DataDir) || c.NodeID == "" || c.ControlURL != o.ManagementURL || c.Installation != o.Installation {
		return nil, fmt.Errorf("saved routing belongs to a different component or control")
	}
	return c.Routes, nil
}

// An authoritative empty configuration replaces the cache too. Failed fetches
// and failed Caddy reloads must never alter this last successfully applied set.
func saveRouteCache(o Options, routes []proxy.Route) error {
	b, err := json.Marshal(routeCache{NodeID: loadBalancerNodeID(o.DataDir), ControlURL: o.ManagementURL, Installation: o.Installation, Routes: routes})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(o.DataDir, ".routes-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if err = f.Chmod(0600); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(o.DataDir, "loadbalancer-routes.json")); err != nil {
		return err
	}
	dir, err := os.Open(o.DataDir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
