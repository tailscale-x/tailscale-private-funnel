package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
)

func registerRuntime(ctx context.Context, mode string, o *Options, c tsconnection.Connection) (tsconnection.Connection, error) {
	st, e := c.Up(ctx)
	if e != nil {
		return nil, e
	}
	if st.Self == nil {
		return nil, errors.New("Tailscale identity unavailable")
	}
	role := identity.Role(mode)
	if mode == "k8s-forwarder" {
		role = identity.KubernetesGateway
	}
	var tags []string
	if st.Self.Tags != nil {
		tags = st.Self.Tags.AsSlice()
	}
	if e = identity.Authorize(o.Installation, role, tags); e != nil {
		return nil, e
	}
	if o.ComponentID == "" {
		b, e := os.ReadFile(filepath.Join(o.DataDir, "node-id"))
		if e == nil {
			o.ComponentID = strings.TrimSpace(string(b))
		} else if !os.IsNotExist(e) {
			return nil, e
		}
	}
	if o.ComponentID == "" {
		o.ComponentID = uuid.NewString()
	}
	if e = os.WriteFile(filepath.Join(o.DataDir, "node-id"), []byte(o.ComponentID), 0600); e != nil {
		return nil, e
	}
	if o.InstanceID == "" {
		o.InstanceID = uuid.NewString()
	}
	d := &v1.DeploymentRequest{Mode: mode, Installation: o.Installation, Name: o.Hostname, Hostname: o.Hostname, ResourceName: os.Getenv("FUNNEL_RESOURCE_NAME"), Namespace: o.KubeNamespace, Version: BuildVersion, StateDir: o.StateDir, DataDir: o.DataDir, PublicHostname: o.LoadBalancerHostname, CertificateEmail: o.CertEmail, LabelKeys: o.LabelKeys, DenyNetworks: o.DenyNetworks, TailscaleConnection: o.TailscaleConnection, Target: "native"}
	if o.KubeNamespace != "" {
		d.Target = "kubernetes"
	}
	if d.ResourceName == "" {
		d.ResourceName = o.ComponentID
	} else {
		d.Name = d.ResourceName
	}
	nodeUID := ""
	if mode == "k8s-forwarder" {
		d.Mode = "k8s-gateway"
		nodeUID = o.NodeUID
	}
	rpc := funnelv1connect.NewNodeServiceClient(tsnetHTTPClient(c), o.ManagementURL)
	registerCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for {
		_, e = rpc.RegisterComponent(registerCtx, connect.NewRequest(&v1.RegisterComponentRequest{ComponentId: o.ComponentID, InstanceId: o.InstanceID, Deployment: d, ParentId: o.ParentID, NodeUid: nodeUID}))
		if e == nil {
			break
		}
		code := connect.CodeOf(e)
		if code != connect.CodeAborted && code != connect.CodeUnavailable && code != connect.CodeDeadlineExceeded {
			return nil, e
		}
		select {
		case <-registerCtx.Done():
			return nil, registerCtx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return &tsconnection.Bound{Connection: c, ComponentID: o.ComponentID, InstanceID: o.InstanceID}, nil
}
