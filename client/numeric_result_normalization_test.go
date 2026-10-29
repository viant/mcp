package client

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/viant/mcp-protocol/schema"
)

func TestNormalizeResultJSONPreservesOpaqueNumbers(t *testing.T) {
	const numbers = `{"small":7,"large":9007199254740993,"maxUint64":18446744073709551615,"negative":-9007199254740993,"fraction":0.12345678901234567890123456789,"exponent":1.234567890123456789e+100,"overflow":1e400,"nested":[9007199254740993,null,[18446744073709551615,1e-400]],"text":"9007199254740993","bool":true}`
	for _, method := range []string{schema.MethodToolsCall, schema.MethodToolsList, schema.MethodResourcesRead} {
		t.Run(method, func(t *testing.T) {
			raw := json.RawMessage(`{"content":[{"type":"text","text":"exact"}],"structuredContent":` + numbers + `,"_meta":{"opaque":` + numbers + `}}`)
			normalized := normalizeResultJSON(method, raw)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(normalized, &fields); err != nil {
				t.Fatal(err)
			}
			if string(fields["resultType"]) != `"complete"` {
				t.Fatalf("missing default result type: %s", normalized)
			}
			if method != schema.MethodToolsCall && (string(fields["cacheScope"]) != `"private"` || string(fields["ttlMs"]) != "0") {
				t.Fatalf("cache defaults changed: %s", normalized)
			}
			var meta map[string]json.RawMessage
			if err := json.Unmarshal(fields["_meta"], &meta); err != nil {
				t.Fatal(err)
			}
			for _, actual := range []json.RawMessage{fields["structuredContent"], meta["opaque"]} {
				if !reflect.DeepEqual(normalizationNumberOracle(t, []byte(numbers)), normalizationNumberOracle(t, actual)) {
					t.Fatalf("opaque numeric data changed: %s", normalized)
				}
				var object map[string]json.RawMessage
				if err := json.Unmarshal(actual, &object); err != nil {
					t.Fatal(err)
				}
				if string(object["large"]) != "9007199254740993" || string(object["maxUint64"]) != "18446744073709551615" {
					t.Fatalf("integer changed or became a string: %s", normalized)
				}
			}
		})
	}
}

func TestNormalizeResultJSONDefaultsRemainTopLevel(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, []byte(`null`), []byte(`[]`), []byte(`{"resultType":`), []byte(`{} {}`)} {
		if actual := normalizeResultJSON(schema.MethodToolsCall, raw); !bytes.Equal(actual, raw) {
			t.Fatalf("invalid/non-object result changed: %s -> %s", raw, actual)
		}
	}
	for _, raw := range []string{`{"resultType":"input_required","cacheScope":"public","ttlMs":123}`, `{"resultType":null,"cacheScope":null,"ttlMs":null}`} {
		if !reflect.DeepEqual(normalizationNumberOracle(t, []byte(raw)), normalizationNumberOracle(t, normalizeResultJSON(schema.MethodToolsList, []byte(raw)))) {
			t.Fatalf("authoritative values changed: %s", raw)
		}
	}
	var fields map[string]json.RawMessage
	normalized := normalizeResultJSON(schema.MethodToolsList, []byte(`{"resultType":"","cacheScope":"","ttlMs":0}`))
	if err := json.Unmarshal(normalized, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["resultType"]) != `"complete"` || string(fields["cacheScope"]) != `"private"` || string(fields["ttlMs"]) != "0" {
		t.Fatalf("legacy empty defaults changed: %s", normalized)
	}
}

func normalizationNumberOracle(t *testing.T, raw []byte) interface{} {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result interface{}
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestNormalizeResultJSONPreservesFiniteIntegerOnWire(t *testing.T) {
	raw := json.RawMessage(`{"resultType":"complete","structuredContent":{"id":9007199254740993}}`)
	normalized := normalizeResultJSON(schema.MethodToolsCall, raw)
	var envelope struct {
		Structured map[string]json.RawMessage `json:"structuredContent"`
	}
	if err := json.Unmarshal(normalized, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope.Structured["id"]) != "9007199254740993" {
		t.Fatalf("finite integer changed or became a string: %s", normalized)
	}
}
