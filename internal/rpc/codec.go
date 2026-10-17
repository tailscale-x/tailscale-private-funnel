// Package rpc contains shared transport policies, not application authorization.
package rpc

import (
	"fmt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// StrictJSON rejects removed/misspelled fields instead of silently ignoring them.
type StrictJSON struct{}

func (StrictJSON) Name() string { return "json" }
func (StrictJSON) Marshal(value any) ([]byte, error) {
	m, ok := value.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("protobuf message required")
	}
	return protojson.Marshal(m)
}
func (StrictJSON) Unmarshal(data []byte, value any) error {
	m, ok := value.(proto.Message)
	if !ok {
		return fmt.Errorf("protobuf message required")
	}
	return (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, m)
}
