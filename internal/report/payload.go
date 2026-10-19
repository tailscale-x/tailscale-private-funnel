package report

import (
	"encoding/json"

	"google.golang.org/protobuf/proto"
)

func Decode(s Snapshot, out proto.Message) error { return json.Unmarshal(s.Payload, out) }
func Payload[T proto.Message](v any, out T) (T, error) {
	b, e := json.Marshal(v)
	if e == nil {
		e = json.Unmarshal(b, out)
	}
	return out, e
}
