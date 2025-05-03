package schema

import (
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
)

// MustParseParams parses JSON-RPC request parameters into the provided struct.
func MustParseParams[T any](req *jsonrpc.Request, resp *jsonrpc.Response, v *T) bool {
	if err := json.Unmarshal(req.Params, v); err != nil {
		resp.Error = jsonrpc.NewInvalidParamsError(
			fmt.Sprintf("failed to parse params: %v", err), req.Params)
		return false
	}
	return true
}
