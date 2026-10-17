package report

import (
	"encoding/base64"
	"errors"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

func Wire(e Envelope) (*v1.SignedEnvelope, error) {
	pub, err := base64.RawStdEncoding.DecodeString(e.PublicKey)
	if err != nil {
		return nil, err
	}
	signature, err := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil {
		return nil, err
	}
	return &v1.SignedEnvelope{NodeId: e.NodeID, Timestamp: e.Timestamp, Nonce: e.Nonce, PublicKey: pub, Payload: e.Payload, Signature: signature, Purpose: e.Purpose}, nil
}
func FromWire(e *v1.SignedEnvelope, purpose string) (Envelope, error) {
	if e == nil || e.Purpose != purpose || len(e.PublicKey) != 32 || len(e.Signature) != 64 || len(e.Payload) == 0 || e.NodeId == "" {
		return Envelope{}, errors.New("invalid signed envelope or message purpose")
	}
	return Envelope{NodeID: e.NodeId, Timestamp: e.Timestamp, Nonce: e.Nonce, PublicKey: base64.RawStdEncoding.EncodeToString(e.PublicKey), Payload: e.Payload, Signature: base64.StdEncoding.EncodeToString(e.Signature), Purpose: e.Purpose}, nil
}
