package app

import (
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"os"
	"path/filepath"
	"testing"
)

func TestAppliedRouteCacheSurvivesRestartAndHonorsWithdrawal(t *testing.T) {
	o := Options{DataDir: t.TempDir(), ManagementURL: "http://control.example.ts.net:8080", Installation: "main"}
	if err := os.WriteFile(filepath.Join(o.DataDir, "node-id"), []byte("edge"), 0600); err != nil {
		t.Fatal(err)
	}
	routes := []proxy.Route{{Hostname: "app.example.com", Service: "app.example.ts.net", Port: 8080}}
	if err := saveRouteCache(o, routes); err != nil {
		t.Fatal(err)
	}
	got, err := loadRouteCache(o)
	if err != nil || len(got) != 1 || got[0].Hostname != routes[0].Hostname {
		t.Fatal(got, err)
	}
	info, err := os.Stat(filepath.Join(o.DataDir, "loadbalancer-routes.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	for _, change := range []string{"control", "installation", "node"} {
		t.Run(change, func(t *testing.T) {
			changed := o
			switch change {
			case "control":
				changed.ManagementURL = "http://other.example.ts.net:8080"
			case "installation":
				changed.Installation = "other"
			case "node":
				if err := os.WriteFile(filepath.Join(o.DataDir, "node-id"), []byte("different"), 0600); err != nil {
					t.Fatal(err)
				}
				defer os.WriteFile(filepath.Join(o.DataDir, "node-id"), []byte("edge"), 0600)
			}
			if _, err := loadRouteCache(changed); err == nil {
				t.Fatal("foreign cache accepted")
			}
		})
	}
	if err := saveRouteCache(o, nil); err != nil {
		t.Fatal(err)
	}
	got, err = loadRouteCache(o)
	if err != nil || len(got) != 0 {
		t.Fatal("authoritative withdrawal resurrected routes", got, err)
	}
	if err = os.WriteFile(filepath.Join(o.DataDir, "loadbalancer-routes.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadRouteCache(o); err == nil {
		t.Fatal("corrupt cache accepted")
	}
}

func TestStartupUsesAppliedRoutesBeforeAnyControlRequest(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "working", true: "withdrawn"}[empty], func(t *testing.T) {
			o := Options{DataDir: t.TempDir(), ManagementURL: "http://control.example.ts.net:8080", Installation: "main"}
			if err := os.WriteFile(filepath.Join(o.DataDir, "node-id"), []byte("edge"), 0600); err != nil {
				t.Fatal(err)
			}
			routes := []proxy.Route{{Hostname: "app.example.com", Service: "app.example.ts.net", Port: 8080}}
			if empty {
				routes = nil
			}
			if err := saveRouteCache(o, routes); err != nil {
				t.Fatal(err)
			}
			fetch := func() ([]proxy.Route, error) {
				t.Fatal("startup contacted control despite valid applied routes")
				return nil, nil
			}
			got, err := initialLoadBalancerRoutes(o, fetch)
			if err != nil || len(got) != len(routes) {
				t.Fatal(got, err)
			}
		})
	}
	o := Options{DataDir: t.TempDir()}
	calls := 0
	_, err := initialLoadBalancerRoutes(o, func() ([]proxy.Route, error) { calls++; return nil, os.ErrNotExist })
	if err == nil || calls != 1 {
		t.Fatal("cold startup must make exactly one control request", calls, err)
	}
}
