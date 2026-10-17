package app

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"fmt"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	ts "github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"github.com/tailscale-x/tailscale-private-funnel/internal/web"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"tailscale.com/ipn"
	"tailscale.com/tsnet"
)

// Cleanup performs remote revocation while durable credentials still exist.
// The deployment owner removes local files only after this function succeeds.
func Cleanup(ctx context.Context, mode string, o Options) error {
	for _, key := range []string{"TS_AUTHKEY", "TS_AUTH_KEY", "TS_CLIENT_SECRET", "TS_CLIENT_ID", "TSNET_FORCE_LOGIN"} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("unset %s before cleanup; cleanup must never reenroll", key)
		}
	}
	var err error
	o, err = defaults(mode, o)
	if err != nil {
		return err
	}
	if mode != "control" {
		o, err = loadConfig(mode, o)
		if err != nil {
			return err
		}
	}
	if o.TSStore == nil {
		if err = validatePersistentState(mode, o); err != nil {
			return fmt.Errorf("cleanup requires existing identity: %w", err)
		}
	}
	var api ts.Client
	if mode == "control" {
		st, e := openRuntimeStore(o)
		if e != nil {
			return e
		}
		defer st.Close()
		nodes, e := st.Nodes("")
		if e != nil {
			return e
		}
		for _, n := range nodes {
			var cleanup struct {
				Pending bool `json:"pending"`
			}
			if st.GetJSON("node_cleanup:"+n["id"].(string), &cleanup) == nil && cleanup.Pending {
				return errors.New("pending node cleanup tasks remain; keep control running until they complete")
			}
			if revoked, _ := n["revoked"].(bool); !revoked {
				return errors.New("control has active enrolled components; delete or revoke them first")
			}
		}
		api.ClientID, e = st.GetSecret("tailscale_oauth_client_id")
		if e != nil {
			return e
		}
		api.ClientSecret, e = st.GetSecret("tailscale_oauth_client_secret")
		if e != nil {
			return e
		}
		var tailnet struct {
			Name string `json:"name"`
		}
		if e = st.GetJSON("tailnet", &tailnet); e != nil {
			return e
		}
		api.Tailnet = tailnet.Name
		if e = (&web.Control{Store: st}).CleanupOwnedInstallation(ctx); e != nil {
			return e
		}
	}
	s := &tsnet.Server{Hostname: o.Hostname, Dir: o.StateDir, Ephemeral: false, Store: o.TSStore, UserLogf: func(string, ...any) {}}
	defer s.Close()
	if mode != "control" && cleanupConfirmed(o) {
		if err := s.Start(); err != nil {
			return err
		}
		return verifyDeviceRevocation(ctx, s, o.ManagementURL)
	}
	status, err := s.Up(ctx)
	if err != nil {
		return fmt.Errorf("cleanup identity unavailable: %w", err)
	}
	if mode != "control" {
		if strings.TrimSpace(o.ManagementURL) == "" {
			return errors.New("cleanup requires the enrolled management URL")
		}
		return notifyNodeShutdown(ctx, s, o)
	}
	if status.Self == nil {
		return errors.New("control identity unavailable")
	}
	return api.DeleteDevice(ctx, string(status.Self.ID))
}
func CleanupKubernetesNode(ctx context.Context, mode string, o Options) error {
	var err error
	o, err = kubernetesNodeOptions(mode, o)
	if err != nil {
		return err
	}
	c, _, err := kubernetesClients(o)
	if err != nil {
		return err
	}
	var secret core.Secret
	if err = c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}, &secret); err != nil {
		return err
	}
	o, err = restoreNodeBundle(mode, o, secret)
	if err != nil {
		return err
	}
	o.TSStore = kubebroker.SecretStore{Client: c, Namespace: o.KubeNamespace, Name: o.IdentitySecret}
	return Cleanup(ctx, mode, o)
}

const cleanupReceiptKey ipn.StateKey = "funnel-cleanup-confirmed"

func cleanupConfirmed(o Options) bool {
	if o.TSStore != nil {
		value, err := o.TSStore.ReadState(cleanupReceiptKey)
		return err == nil && string(value) == o.ManagementURL
	}
	value, err := os.ReadFile(filepath.Join(o.DataDir, "cleanup-confirmed"))
	return err == nil && string(value) == o.ManagementURL
}
func persistCleanupConfirmation(o Options) error {
	if o.TSStore != nil {
		return o.TSStore.WriteState(cleanupReceiptKey, []byte(o.ManagementURL))
	}
	return os.WriteFile(filepath.Join(o.DataDir, "cleanup-confirmed"), []byte(o.ManagementURL), 0600)
}

func notifyNodeShutdown(ctx context.Context, s *tsnet.Server, o Options) error {
	rpc := funnelv1connect.NewNodeServiceClient(s.HTTPClient(), o.ManagementURL)
	response, e := rpc.Shutdown(ctx, connect.NewRequest(&v1.ShutdownRequest{}))
	if e != nil {
		return e
	}
	if !response.Msg.Ok {
		return errors.New(response.Msg.Message)
	}
	// Persist proof of successful operational withdrawal before the final device
	// deletion request can cut off this identity. Retrying cleanup never reenrolls.
	if e = persistCleanupConfirmation(o); e != nil {
		return e
	}
	confirmed, e := rpc.Shutdown(ctx, connect.NewRequest(&v1.ShutdownRequest{Confirm: true}))
	if e != nil {
		return e
	}
	if !confirmed.Msg.Ok {
		return errors.New(confirmed.Msg.Message)
	}
	return verifyDeviceRevocation(ctx, s, o.ManagementURL)
}

// A running control connection can retain a stale Running state after API
// device deletion. Reconnect the same durable identity, without an auth key,
// to obtain the coordination server's current authorization decision. Never run
// two servers over the same state and never infer revocation from a timeout.
func verifyDeviceRevocation(ctx context.Context, original *tsnet.Server, url string) error {
	_ = original.Close()
	confirmationRetried := false
	for {
		probe := &tsnet.Server{Dir: original.Dir, Store: original.Store, Hostname: original.Hostname,
			ControlURL: original.ControlURL, Ephemeral: false, UserLogf: func(string, ...any) {}}
		if err := probe.Start(); err != nil {
			return err
		}
		local, err := probe.LocalClient()
		if err != nil {
			probe.Close()
			return err
		}
		deadline := time.NewTimer(15 * time.Second)
		tick := time.NewTicker(time.Second)
		retry := false
		for !retry {
			status, e := local.StatusWithoutPeers(ctx)
			if e == nil && (status.BackendState == "NeedsLogin" && status.AuthURL != "") {
				deadline.Stop()
				tick.Stop()
				probe.Close()
				return nil
			}
			if e == nil && status.BackendState == "Running" && !confirmationRetried {
				confirmationRetried = true
				rpc := funnelv1connect.NewNodeServiceClient(probe.HTTPClient(), url)
				retryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_, _ = rpc.Shutdown(retryCtx, connect.NewRequest(&v1.ShutdownRequest{Confirm: true}))
				cancel()
			}
			select {
			case <-ctx.Done():
				deadline.Stop()
				tick.Stop()
				probe.Close()
				return fmt.Errorf("waiting for confirmed device revocation; identity retained: %w", ctx.Err())
			case <-deadline.C:
				retry = true
			case <-tick.C:
			}
		}
		tick.Stop()
		probe.Close()
	}
}

// ControlDeletionReady inspects the database without starting another Tailscale identity.
func ControlDeletionReady(o Options) error {
	var e error
	o, e = defaults("control", o)
	if e != nil {
		return e
	}
	if _, e = os.Stat(filepath.Join(o.DataDir, "funnel.db")); os.IsNotExist(e) {
		return nil
	}
	st, e := openRuntimeStore(o)
	if e != nil {
		return e
	}
	defer st.Close()
	nodes, e := st.Nodes("")
	if e != nil {
		return e
	}
	for _, n := range nodes {
		if !n["revoked"].(bool) {
			return errors.New("control has active enrolled components; delete or revoke them first")
		}
		var state struct {
			Pending bool `json:"pending"`
		}
		if st.GetJSON("node_cleanup:"+n["id"].(string), &state) == nil && state.Pending {
			return errors.New("control has pending node cleanups; wait before deleting control")
		}
	}
	return nil
}
