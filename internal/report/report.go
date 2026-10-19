// Package report contains authenticated runtime snapshots. Authentication is
// supplied by the Tailscale connection, never by payload keys or signatures.
package report

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

type Snapshot struct {
	NodeID    string          `json:"node_id"`
	Timestamp int64           `json:"timestamp"`
	Nonce     string          `json:"nonce"`
	Payload   json.RawMessage `json:"payload"`
	Purpose   string          `json:"purpose"`
}

func NewSnapshot(purpose, nodeID string, payload any, now time.Time) (Snapshot, error) {
	b, e := json.Marshal(payload)
	if e != nil {
		return Snapshot{}, e
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return Snapshot{}, e
	}
	return Snapshot{NodeID: nodeID, Timestamp: now.Unix(), Nonce: hex.EncodeToString(nonce), Payload: b, Purpose: purpose}, nil
}
func (s Snapshot) Validate(now time.Time, maxAge time.Duration) error {
	if s.NodeID == "" || s.Purpose == "" || len(s.Payload) == 0 {
		return errors.New("invalid snapshot")
	}
	if s.Timestamp > now.Add(time.Minute).Unix() || s.Timestamp < now.Add(-maxAge).Unix() {
		return errors.New("snapshot is stale")
	}
	return nil
}
