package server

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestAdapterResultDefaultsPreserveOpaquePayloadAndUnknownMetadata(t *testing.T) {
	exact := []byte("{\n  \"value\":18446744073709551615,\"text\":\"<>&\\u003c\\\\escape\"\n}")
	raw := json.RawMessage(`{"unknown":{"keep":["<>&",18446744073709551615]},"payload":{"number":18446744073709551615,"html":"<>&\u003c","slash":"\\escape"},"definitionBytes":"` + base64.StdEncoding.EncodeToString(exact) + `"}`)
	var decoded struct {
		ResultType string          `json:"resultType"`
		CacheScope string          `json:"cacheScope"`
		TTL        int             `json:"ttlMs"`
		Unknown    json.RawMessage `json:"unknown"`
		Payload    json.RawMessage `json:"payload"`
		Bytes      []byte          `json:"definitionBytes"`
	}
	if err := decodeAdapterResult(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ResultType != completeResultType || decoded.CacheScope != "private" || decoded.TTL != 0 {
		t.Fatalf("missing stateless defaults: %+v", decoded)
	}
	if string(decoded.Bytes) != string(exact) {
		t.Fatal("opaque lossless definition carrier changed")
	}
	var payload struct {
		Number json.Number `json:"number"`
		HTML   string      `json:"html"`
		Slash  string      `json:"slash"`
	}
	if err := json.Unmarshal(decoded.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Number.String() != "18446744073709551615" || payload.HTML != "<>&<" || payload.Slash != "\\escape" {
		t.Fatalf("opaque payload lost integer/text compatibility: %+v", payload)
	}
	var unknown struct {
		Keep []json.RawMessage `json:"keep"`
	}
	if err := json.Unmarshal(decoded.Unknown, &unknown); err != nil || len(unknown.Keep) != 2 || string(unknown.Keep[1]) != "18446744073709551615" {
		t.Fatalf("unknown metadata lost: %s %v", decoded.Unknown, err)
	}
}
func TestAdapterResultKeepsExplicitDefaultsNullAndInvalidInput(t *testing.T) {
	for _, raw := range []string{`{"resultType":"custom","cacheScope":"public","ttlMs":57}`, `{"resultType":null,"cacheScope":null,"ttlMs":null}`} {
		var value map[string]json.RawMessage
		if err := decodeAdapterResult(json.RawMessage(raw), &value); err != nil {
			t.Fatal(err)
		}
		var expected map[string]json.RawMessage
		_ = json.Unmarshal([]byte(raw), &expected)
		for key, wanted := range expected {
			if string(value[key]) != string(wanted) {
				t.Fatalf("explicit metadata changed %s: %s != %s", key, value[key], wanted)
			}
		}
	}
	var value map[string]json.RawMessage
	if err := decodeAdapterResult(json.RawMessage(`{"resultType":"","cacheScope":""}`), &value); err != nil {
		t.Fatal(err)
	}
	if string(value["resultType"]) != `"complete"` || string(value["cacheScope"]) != `"private"` {
		t.Fatalf("empty metadata not defaulted: %v", value)
	}
	if err := decodeAdapterResult(json.RawMessage(`{"broken":`), &value); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}
