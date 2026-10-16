package server

import (
	"bytes"
	"encoding/json"
	"github.com/viant/mcp-protocol/schema"
)

// decodeAdapterResult applies only top-level stateless result defaults. Opaque
// nested payloads stay RawMessage: decoding them into interface{} and reencoding
// costs multiple full-body conversions and can lose large integer precision.
func decodeAdapterResult(raw json.RawMessage, destination interface{}) error {
	var object map[string]json.RawMessage
	legacyToolHints := false
	if len(raw) > 0 && json.Unmarshal(raw, &object) == nil && object != nil {
		if _, ok := destination.(*schema.ListToolsResult); ok {
			scope, present := object["cacheScope"]
			_, ttlPresent := object["ttlMs"]
			legacyToolHints = (!present || bytes.Equal(bytes.TrimSpace(scope), []byte(`""`))) && !ttlPresent
		}
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
	if err := json.Unmarshal(raw, destination); err != nil {
		return err
	}
	if legacyToolHints {
		destination.(*schema.ListToolsResult).CacheScope = ""
	}
	return nil
}
