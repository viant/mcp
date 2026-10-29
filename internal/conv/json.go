package conv

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/viant/mcp-protocol/jsonvalue"
	"io"
)

// DecodeNumbers preserves JSON numeric values when request arguments pass
// through metadata adapters. Generic map/interface targets retain legacy float64
// leaves when their JSON decimal value is unchanged. Struct owners normalize
// their generic fields explicitly. It accepts one JSON value, like json.Unmarshal.
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
	switch value := target.(type) {
	case *map[string]interface{}:
		jsonvalue.Normalize(*value)
	case *interface{}:
		*value = jsonvalue.Normalize(*value)
	}
	return nil
}
