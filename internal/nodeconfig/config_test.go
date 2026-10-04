package nodeconfig

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"testing"
	"time"
)

func TestVerifyRequiresPinnedControlKey(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	config := Configuration{NodeID: "ingress-1", Revision: "rev-1", IssuedAt: time.Now().Unix()}
	e, err := report.NewEnvelope(config.NodeID, config, pub, priv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(e, base64.RawStdEncoding.EncodeToString(pub), config.NodeID, time.Now()); err != nil {
		t.Fatal(err)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(e, base64.RawStdEncoding.EncodeToString(other), config.NodeID, time.Now()); err == nil {
		t.Fatal("accepted configuration signed by another key")
	}
}
