package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kube"
	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"io"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"net/http"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sync"
	"sync/atomic"
	"time"
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
func BootstrapKubernetesController(ctx context.Context, o Options) error {
	c, _, e := kubernetesClients(o)
	if e != nil {
		return e
	}
	var existing core.Secret
	if e = c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.Installation + "-controller-state"}, &existing); e == nil {
		return nil
	}
	if client.IgnoreNotFound(e) != nil {
		return e
	}
	o.InstallService = false
	if e = Deploy(ctx, "k8s-gateway", o); e != nil {
		return e
	}
	id, e := store.NewResourceID()
	if e != nil {
		return e
	}
	if _, statErr := os.Stat(filepath.Join(o.DataDir, "cluster-id")); os.IsNotExist(statErr) {
		if e = os.WriteFile(filepath.Join(o.DataDir, "cluster-id"), []byte(id), 0600); e != nil {
			return e
		}
	}
	return kubebroker.ImportController(ctx, c, o.KubeNamespace, o.Installation+"-controller-state", o.DataDir, o.StateDir)
}
func ServeKubernetesController(ctx context.Context, o Options) error {
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
	o.ConfigFile = filepath.Join(o.DataDir, "config.json")
	if e = kubebroker.RestoreController(ctx, c, o.KubeNamespace, o.Installation+"-controller-state", o.DataDir, o.StateDir); e != nil {
		return e
	}
	var saved savedConfig
	b, e := os.ReadFile(o.ConfigFile)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, &saved); e != nil {
		return e
	}
	dns := o.Installation + "-broker." + o.KubeNamespace + ".svc"
	cert, _, e := kubebroker.EnsureTLS(ctx, c, o.KubeNamespace, o.Installation+"-broker-tls", dns)
	if e != nil {
		return e
	}
	broker := &kubebroker.Broker{Kube: c, Tokens: k, Namespace: o.KubeNamespace, Installation: o.Installation, DaemonSet: o.Installation + "-forwarder", ServiceAccount: o.Installation + "-forwarder", Audience: "tailscale-private-funnel/" + o.Installation, ManagementURL: saved.ManagementURL, ControlKey: saved.ControlKey, Ready: func() bool { return ctx.Err() == nil && controllerHTTP.Load() != nil }}
	broker.ControllerID = loadBalancerNodeID(o.DataDir)
	broker.Configuration = func(ctx context.Context) (report.Envelope, error) {
		var envelope report.Envelope
		value := controllerHTTP.Load()
		if value == nil {
			return envelope, fmt.Errorf("controller joining")
		}
		req, err := http.NewRequestWithContext(ctx, "GET", saved.ManagementURL+"/api/node/config", nil)
		if err != nil {
			return envelope, err
		}
		res, err := value.(*http.Client).Do(req)
		if err != nil {
			return envelope, err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return envelope, fmt.Errorf("configuration HTTP %d", res.StatusCode)
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&envelope)
		return envelope, err
	}
	var inventoryMu sync.Mutex
	inventory := map[string]bool{}
	inventoryAt := time.Time{}
	broker.Allowed = func(ctx context.Context, id string) (bool, error) {
		inventoryMu.Lock()
		defer inventoryMu.Unlock()
		if time.Since(inventoryAt) > 5*time.Second {
			value := controllerHTTP.Load()
			if value == nil {
				return false, fmt.Errorf("controller joining")
			}
			req, e := http.NewRequestWithContext(ctx, "GET", saved.ManagementURL+"/api/node/forwarders", nil)
			if e != nil {
				return false, e
			}
			res, e := value.(*http.Client).Do(req)
			if e != nil {
				return false, e
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				return false, fmt.Errorf("controller authorization HTTP %d", res.StatusCode)
			}
			var nodes []store.NodeRecord
			if e = json.NewDecoder(res.Body).Decode(&nodes); e != nil {
				return false, e
			}
			inventory = map[string]bool{}
			for _, n := range nodes {
				inventory[n.ID] = !n.Revoked
			}
			inventoryAt = time.Now()
		}
		return inventory[id], nil
	}
	// Broker requests use the singleton controller's Service endpoint.
	broker.Enroll = func(ctx context.Context, uid, hostname, public string) (kubebroker.Enrollment, error) {
		value := controllerHTTP.Load()
		if value == nil {
			return kubebroker.Enrollment{}, fmt.Errorf("controller joining")
		}
		agent := report.Agent{NodeID: loadBalancerNodeID(o.DataDir), ManagementURL: saved.ManagementURL, DataDir: o.DataDir, HTTPClient: value.(*http.Client)}
		response, e := agent.SnapshotResponse(ctx, "/api/node/forwarders", map[string]string{"node_uid": uid, "hostname": hostname, "public_key": public})
		if e != nil {
			return kubebroker.Enrollment{}, e
		}
		var out struct {
			NodeID  string `json:"node_id"`
			AuthKey string `json:"auth_key"`
		}
		e = json.Unmarshal(response, &out)
		return kubebroker.Enrollment{NodeID: out.NodeID, AuthKey: out.AuthKey}, e
	}
	server := &http.Server{Addr: ":9443", Handler: broker.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: kubebroker.RotatingCertificate(c, o.KubeNamespace, o.Installation+"-broker-tls", dns, cert)}}
	defer server.Close()
	o.TSStore = kubebroker.SecretStore{Client: c, Namespace: o.KubeNamespace, Name: o.Installation + "-controller-state"}
	// The chart runs one Recreate Deployment. The sole controller owns this
	// Secret-backed identity; forwarder identity leases remain independent.
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
