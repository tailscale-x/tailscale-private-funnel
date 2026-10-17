package report

import (
	"bytes"
	"encoding/json"
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
	e, err := NewEnvelope("gateway-1", map[string]any{"host": "app.example"}, pub, priv, now)
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
	e, _ := NewEnvelope("gateway-1", map[string]any{"host": "app.example"}, pub, priv, now)
	e.Payload = []byte(`{"host":"evil.example"}`)
	if _, _, err := Verify(e, now, time.Minute); err == nil {
		t.Fatal("tampered payload accepted")
	}
}

func TestEnvelopeRejectsStale(t *testing.T) {
	pub, priv, _ := GenerateKey()
	now := time.Unix(1700000000, 0)
	e, _ := NewEnvelope("gateway-1", map[string]any{}, pub, priv, now)
	if _, _, err := Verify(e, now.Add(2*time.Minute), time.Minute); err == nil {
		t.Fatal("stale report accepted")
	}
}

func TestSignedBytesSurvivePrettyJSONAndPurposeBinding(t *testing.T) {
	pub, key, e := GenerateKey()
	if e != nil {
		t.Fatal(e)
	}
	payload := json.RawMessage("{\n  \"exposures\": [],\n  \"expires_at\": 123\n}")
	env, e := NewEnvelopeWithPurpose("peers", "node", payload, pub, key, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	wire, e := Wire(env)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = FromWire(wire, "report"); e == nil {
		t.Fatal("cross-purpose replay accepted")
	}
	encoded, e := json.MarshalIndent(env, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	var restored Envelope
	if e = json.Unmarshal(encoded, &restored); e != nil {
		t.Fatal(e)
	}
	if _, _, e = Verify(restored, time.Now(), time.Minute); e != nil {
		t.Fatal(e)
	}
	if string(env.Payload) != string(restored.Payload) {
		t.Fatal("signed payload bytes changed")
	}
}
