package report

import (
	"time"

	"buf.build/go/protovalidate"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// FromWireTyped verifies the exact bytes before decoding their declared model.
// Business handlers still enforce caller/key binding, nonce uniqueness and policy.
func FromWireTyped(wire *v1.SignedEnvelope, purpose string, model proto.Message) (Envelope, error) {
	envelope, err := FromWire(wire, purpose)
	if err != nil {
		return Envelope{}, err
	}
	if _, _, err = Verify(envelope, time.Now(), 5*time.Minute); err != nil {
		return Envelope{}, err
	}
	if err = (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(envelope.Payload, model); err != nil {
		return Envelope{}, err
	}
	if err = protovalidate.Validate(model); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}
