package nodeconfig

import (
	"testing"
	"time"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

func TestConfigurationFreshnessAndTarget(t *testing.T) {
	now := time.Now()
	p := &v1.ConfigurationPayload{NodeId: "gateway", Revision: "1", IssuedAt: now.Unix()}
	if _, e := FromPayload(p, "gateway", now); e != nil {
		t.Fatal(e)
	}
	if _, e := FromPayload(p, "other", now); e == nil {
		t.Fatal("foreign logical target accepted")
	}
	if _, e := FromPayload(p, "gateway", now.Add(6*time.Minute)); e == nil {
		t.Fatal("stale configuration accepted")
	}
}
