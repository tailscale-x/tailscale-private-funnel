package gateway

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

// Inventory is independent of backend inspection, networking and Service APIs.
func (c Config) Inventory(ctx context.Context) (*v1.DockerInventoryPayload, error) {
	items, err := c.Docker.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	out := &v1.DockerInventoryPayload{Complete: true, LabelKeys: c.LabelKeys}
	keys := append([]string{ExposureLabel}, c.LabelKeys...)
	for _, item := range items {
		labels := map[string]string{}
		for k, v := range item.Labels {
			for _, key := range keys {
				if k == key || strings.HasPrefix(k, strings.TrimSuffix(key, ".expose")+".routes.") {
					labels[k] = v
				}
			}
		}
		if _, err = ParseLabels(labels, c.LabelKeys); err != nil {
			return nil, err
		}
		out.Containers = append(out.Containers, &v1.DockerInventoryContainer{Id: item.ID, Running: item.State == "running", Labels: labels})
	}
	return out, nil
}

// Revisions survive ordinary restarts and are allocated before transmission.
func NextInventoryRevision(dir string) (int64, error) {
	p := filepath.Join(dir, "docker-inventory-revision")
	raw, err := os.ReadFile(p)
	var rev int64
	if err == nil {
		rev, err = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || rev < 0 {
			return 0, fmt.Errorf("invalid persisted Docker inventory revision")
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	if rev == int64(^uint64(0)>>1) {
		return 0, fmt.Errorf("inventory revision exhausted")
	}
	rev++
	f, err := os.OpenFile(p+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	if _, err = fmt.Fprintln(f, rev); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	if err = os.Rename(p+".tmp", p); err != nil {
		return 0, err
	}
	d, err := os.Open(dir)
	if err != nil {
		return 0, err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return 0, err
	}
	return rev, nil
}
