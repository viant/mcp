package server

import (
	"context"
	"encoding/json"

	"github.com/viant/mcp-protocol/schema"
)

type toolArgumentsContextKey struct{}
type toolArgumentsContext struct {
	request *schema.CallToolRequest
	raw     json.RawMessage
}

// encoding/json merges repeated arguments objects into the decoded map. No
// single member in that envelope can represent the complete decoded input.
type toolArgumentsMember struct {
	raw      json.RawMessage
	repeated bool
}

func (m *toolArgumentsMember) UnmarshalJSON(data []byte) error {
	if m.raw != nil {
		m.repeated = true
		return nil
	}
	return m.raw.UnmarshalJSON(data)
}

// ToolArgumentsJSON returns the original arguments JSON for this exact tool
// invocation after normal request decoding. Existing decoded Arguments and all
// authorization/dispatch behavior are unchanged. The returned bytes are owned
// by the caller. Omitted arguments return nil, true. Repeated arguments members
// and unrelated requests return nil, false.
func ToolArgumentsJSON(ctx context.Context, request *schema.CallToolRequest) (json.RawMessage, bool) {
	if ctx == nil || request == nil {
		return nil, false
	}
	value, ok := ctx.Value(toolArgumentsContextKey{}).(toolArgumentsContext)
	if !ok || value.request != request {
		return nil, false
	}
	return append(json.RawMessage(nil), value.raw...), true
}
