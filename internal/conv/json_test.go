package conv

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodeNumbersMatchesStandardJSON(t *testing.T) {
	const raw = `{"small":7,"large":9007199254740993,"max":18446744073709551615,"fraction":0.12345678901234567890123456789,"exponent":1e3,"tiny":1e-400,"nested":[7,null],"null":null}`
	var got, want interface{}
	if err := json.Unmarshal([]byte(raw), &want); err != nil {
		t.Fatal(err)
	}
	if err := DecodeNumbers([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("decoding differs: %#v", got)
	}
	if got.(map[string]interface{})["large"].(float64) != 9007199254740992 {
		t.Fatal("legacy rounding changed")
	}
	for _, bad := range []string{"", `{"n":`, `{"n":01}`, `{"n":1e400}`, `{} null`, `{} {}`, `{} junk`} {
		if err := DecodeNumbers([]byte(bad), new(interface{})); err == nil {
			t.Fatalf("invalid JSON accepted: %s", bad)
		}
	}
	var typed struct {
		Number json.Number `json:"number"`
		ID     int64       `json:"id"`
	}
	if err := DecodeNumbers([]byte(`{"number":9007199254740993,"id":9007199254740993}`), &typed); err != nil {
		t.Fatal(err)
	}
	if typed.Number != "9007199254740993" || typed.ID != 9007199254740993 {
		t.Fatal("declared numeric fields changed")
	}
}
