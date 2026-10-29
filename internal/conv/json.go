package conv

import "encoding/json"

// DecodeNumbers retains standard json.Unmarshal numeric behavior: generic
// numeric leaves are float64, with ordinary rounding and overflow rejection.
// Typed numeric fields and custom UnmarshalJSON methods own their decoding.
func DecodeNumbers(raw []byte, target interface{}) error {
	return json.Unmarshal(raw, target)
}
