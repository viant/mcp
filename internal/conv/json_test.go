package conv

import (
	"encoding/json"
	"testing"
)

func TestDecodeNumbersPreservesTokensAndRequiresOneValue(t *testing.T) {
	var object map[string]interface{}
	if err := DecodeNumbers([]byte(` {"large":9007199254740993,"nested":[18446744073709551615,null,1e400]} `), &object); err != nil {
		t.Fatal(err)
	}
	if object["large"] != json.Number("9007199254740993") {
		t.Fatalf("large number changed: %#v", object)
	}
	nested := object["nested"].([]interface{})
	if nested[0] != json.Number("18446744073709551615") || nested[1] != nil || nested[2] != json.Number("1e400") {
		t.Fatalf("nested values changed: %#v", nested)
	}
	for _, raw := range []string{"", `{"n":`, `{"n":01}`, `{} null`, `{} {}`, `{} junk`} {
		if err := DecodeNumbers([]byte(raw), new(interface{})); err == nil {
			t.Fatalf("invalid/trailing JSON accepted: %s", raw)
		}
	}
}
