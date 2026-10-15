package server

import (
	"bytes"
	"encoding/json"
)

// decodeAdapterResult applies only top-level stateless result defaults. Opaque
// nested payloads stay RawMessage: decoding them into interface{} and reencoding
// costs multiple full-body conversions and can lose large integer precision.
func decodeAdapterResult(raw json.RawMessage, destination interface{}) error {
	var object map[string]json.RawMessage
	if len(raw) > 0 && json.Unmarshal(raw, &object) == nil && object != nil {
		if value, ok := object["resultType"]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte(`""`)) {
			object["resultType"] = json.RawMessage(`"` + completeResultType + `"`)
		}
		if value, ok := object["cacheScope"]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte(`""`)) {
			object["cacheScope"] = json.RawMessage(`"private"`)
		}
		if _, ok := object["ttlMs"]; !ok {
			object["ttlMs"] = json.RawMessage(`0`)
		}
		normalized, err := json.Marshal(object)
		if err == nil {
			raw = normalized
		}
	}
	return json.Unmarshal(raw, destination)
}
