package report

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	pub, priv, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	e, err := NewEnvelope("ingress-1", map[string]any{"host": "app.example"}, pub, priv, now)
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := Verify(e, now.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if payload["host"] != "app.example" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestLoadOrCreateIdentityPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	pub1, priv1, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	pub2, priv2, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pub1, pub2) || !bytes.Equal(priv1, priv2) {
		t.Fatal("report identity changed")
	}
}

func TestEnvelopeRejectsTampering(t *testing.T) {
	pub, priv, _ := GenerateKey()
	now := time.Unix(1700000000, 0)
	e, _ := NewEnvelope("ingress-1", map[string]any{"host": "app.example"}, pub, priv, now)
	e.Payload = []byte(`{"host":"evil.example"}`)
	if _, _, err := Verify(e, now, time.Minute); err == nil {
		t.Fatal("tampered payload accepted")
	}
}

func TestEnvelopeRejectsStale(t *testing.T) {
	pub, priv, _ := GenerateKey()
	now := time.Unix(1700000000, 0)
	e, _ := NewEnvelope("ingress-1", map[string]any{}, pub, priv, now)
	if _, _, err := Verify(e, now.Add(2*time.Minute), time.Minute); err == nil {
		t.Fatal("stale report accepted")
	}
}
