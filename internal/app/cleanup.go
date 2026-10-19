package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"github.com/tailscale-x/tailscale-private-funnel/internal/web"
	"tailscale.com/tsnet"
)

// Cleanup withdraws application-owned state. It never deletes or reconfigures a
// borrowed Tailscale device. Removing role authority is a Tailscale operation.
func Cleanup(ctx context.Context, mode string, o Options) error {
	var e error
	o, e = defaults(mode, o)
	if e != nil {
		return e
	}
	if mode == "control" {
		if e = ControlDeletionReady(o); e != nil {
			return e
		}
		st, e := openRuntimeStore(o)
		if e != nil {
			return e
		}
		defer st.Close()
		if e = (&web.Control{Store: st, Installation: o.Installation}).CleanupOwnedInstallation(ctx); e != nil {
			return e
		}
		if o.TailscaleConnection == "host" {
			return tsconnection.NewHost(o.TailscaleSocket, o.DataDir).Close()
		}
		return nil
	}
	if o.KubeNamespace == "" {
		o, e = loadConfig(mode, o)
		if e != nil {
			return e
		}
	}
	var c tsconnection.Connection
	if o.TailscaleConnection == "host" {
		c = tsconnection.NewHost(o.TailscaleSocket, o.DataDir)
	} else {
		key := ""
		if o.KubeNamespace != "" {
			key, e = prepareWorkload(ctx, mode, &o)
			if e != nil {
				return e
			}
		} else if e = validatePersistentState(mode, o); e != nil {
			return e
		}
		native := &tsnet.Server{Hostname: o.Hostname, Dir: o.StateDir, AuthKey: key, Ephemeral: o.KubeNamespace != ""}
		c = tsconnection.Wrap(native)
		defer func() { native.AuthKey = "" }()
	}
	defer c.Close()
	c, e = registerRuntime(ctx, mode, &o, c)
	if e != nil {
		return e
	}
	rpc := funnelv1connect.NewNodeServiceClient(c.HTTPClient(), o.ManagementURL)
	response, e := rpc.Shutdown(ctx, connect.NewRequest(&v1.ShutdownRequest{Remove: true}))
	if e != nil {
		return e
	}
	if !response.Msg.Ok {
		return errors.New(response.Msg.Message)
	}
	return nil
}
func CleanupKubernetesNode(ctx context.Context, mode string, o Options) error {
	if mode == "control" {
		var e error
		o, e = kubernetesNodeOptions(mode, o)
		if e != nil {
			return e
		}
		if e = loadControlMaterial(&o); e != nil {
			return e
		}
	}
	return Cleanup(ctx, mode, o)
}
func CleanupKubernetesAgent(ctx context.Context, o Options) error {
	return Cleanup(ctx, "k8s-gateway", o)
}

// Inspection never opens another network identity.
func ControlDeletionReady(o Options) error {
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
		if n["revoked"] != true {
			return fmt.Errorf("control has registered components; delete them before control")
		}
		var state struct {
			Pending bool `json:"pending"`
		}
		if st.GetJSON("node_cleanup:"+n["id"].(string), &state) == nil && state.Pending {
			return errors.New("control has pending owned-resource cleanup")
		}
	}
	return nil
}

func notifyNodeShutdown(ctx context.Context, c tsconnection.Connection, o Options) error {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, e := funnelv1connect.NewNodeServiceClient(c.HTTPClient(), o.ManagementURL).Shutdown(bounded, connect.NewRequest(&v1.ShutdownRequest{}))
	return e
}
