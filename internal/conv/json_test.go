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

func TestDecodeNumbersLegacyExtraction(t *testing.T) {
	var object map[string]interface{}
	if err := DecodeNumbers([]byte(`{"small":7,"fraction":0.1,"exponent":1e3,"null":null,"precise":0.12345678901234567890123456789}`), &object); err != nil {
		t.Fatal(err)
	}
	if object["small"].(float64) != 7 || object["fraction"].(float64) != 0.1 || object["exponent"].(float64) != 1000 || object["null"] != nil || object["precise"] != json.Number("0.12345678901234567890123456789") {
		t.Fatalf("changed values: %#v", object)
	}
}
