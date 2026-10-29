package conv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// DecodeNumbers preserves JSON numeric tokens when request arguments pass
// through metadata adapters. It accepts one JSON value, like json.Unmarshal.
// Custom UnmarshalJSON methods still own their decoding and must not be used
// as an exact-number boundary without separately decoding their raw arguments.
func DecodeNumbers(raw []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}
