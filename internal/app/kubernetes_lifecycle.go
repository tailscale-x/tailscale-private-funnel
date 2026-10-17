package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/kubebroker"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

func CleanupKubernetesAgent(ctx context.Context, o Options) error {
	c, _, err := kubernetesClients(o)
	if err != nil {
		return err
	}
	o.StateDir = "/var/lib/tailscale-private-funnel/state"
	o.DataDir = "/var/lib/tailscale-private-funnel/data"
	o.ConfigFile = o.DataDir + "/config.json"
	if err = kubebroker.RestoreAgentBundle(ctx, c, o.KubeNamespace, o.Installation+"-agent-state", o.DataDir, o.StateDir); err != nil {
		return err
	}
	o.TSStore = kubebroker.SecretStore{Client: c, Namespace: o.KubeNamespace, Name: o.Installation + "-agent-state"}
	return Cleanup(ctx, "k8s-gateway", o)
}

// Publish control deletion prerequisites without granting control workload permissions.
// Only its already-authorized identity Secret is updated.
func publishKubernetesControlState(ctx context.Context, o Options) {
	c, _, err := kubernetesClients(o)
	if err != nil {
		return
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		st, e := openRuntimeStore(o)
		if e == nil {
			nodes, e := st.Nodes("")
			active, pending := 0, 0
			for _, n := range nodes {
				if revoked, _ := n["revoked"].(bool); !revoked {
					active++
				}
				var cleanup struct {
					Pending bool `json:"pending"`
				}
				if st.GetJSON("node_cleanup:"+n["id"].(string), &cleanup) == nil && cleanup.Pending {
					pending++
				}
			}
			_ = st.Close()
			if e == nil {
				_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
					var s core.Secret
					if e = c.Get(ctx, types.NamespacedName{Namespace: o.KubeNamespace, Name: o.IdentitySecret}, &s); e != nil {
						return e
					}
					if s.Data == nil {
						s.Data = map[string][]byte{}
					}
					s.Data["active-components"] = []byte(strconv.Itoa(active))
					s.Data["pending-node-cleanups"] = []byte(strconv.Itoa(pending))
					if b, e := os.ReadFile(o.DataDir + "/runtime-status.json"); e == nil {
						s.Data["runtime-status"] = b
						var status runtimeStatus
						if json.Unmarshal(b, &status) == nil && len(status.IPs) > 0 {
							s.Data["setup-url"] = []byte("http://" + net.JoinHostPort(status.IPs[0], "8080"))
						}
					}
					return c.Update(ctx, &s)
				})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
