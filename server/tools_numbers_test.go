package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	protocolserver "github.com/viant/mcp-protocol/server"
)

type numericToolHandler struct {
	protocolserver.Handler
	call func(*schema.CallToolRequest)
}

func (h *numericToolHandler) CallTool(_ context.Context, r *jsonrpc.TypedRequest[*schema.CallToolRequest]) (*schema.CallToolResult, *jsonrpc.Error) {
	h.call(r.Request)
	return &schema.CallToolResult{}, nil
}
func TestCallToolUsesLegacyNumericArguments(t *testing.T) {
	calls := 0
	h := &Handler{handler: &numericToolHandler{call: func(r *schema.CallToolRequest) {
		calls++
		args := r.Params.Arguments
		for name, want := range map[string]float64{"large": 9007199254740992, "max": 9223372036854775808, "min": -9223372036854775808} {
			if value, ok := args[name].(float64); !ok || value != want {
				t.Fatalf("%s legacy number changed: %T %v", name, args[name], args[name])
			}
		}
		nested := args["nested"].([]any)[0].(map[string]any)["value"]
		if nested != float64(9007199254740996) {
			t.Fatalf("nested rounded: %T %v", nested, nested)
		}
		if args["zero"] != float64(0) || args["fraction"] != float64(1.25) || args["exponent"] != float64(1000) || args["flag"] != false || args["null"] != nil || args["text"] != "1" {
			t.Fatalf("scalar semantics changed: %+v", args)
		}
	}}}
	raw := json.RawMessage(`{"_meta":null,"name":"exact","arguments":{"large":9007199254740993,"max":9223372036854775807,"min":-9223372036854775808,"fraction":1.25,"exponent":1e3,"nested":[{"value":9007199254740995}],"zero":0,"flag":false,"null":null,"text":"1"}}`)
	if _, err := h.CallTool(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: raw}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("tool not called")
	}
	for _, bad := range []string{`null`, `[]`, string(raw) + ` {}`, `{"_meta":null,"name":"exact","arguments":{"value":01}}`} {
		if _, err := h.CallTool(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: json.RawMessage(bad)}); err == nil {
			t.Fatal("invalid numeric/JSON input accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid input reached tool")
	}
}

func TestCallToolMissingAndNullArguments(t *testing.T) {
	for _, suffix := range []string{"", `,"arguments":null`, `,"arguments":{}`} {
		calls := 0
		h := &Handler{handler: &numericToolHandler{call: func(r *schema.CallToolRequest) {
			calls++
			if len(r.Params.Arguments) != 0 {
				t.Fatal("unexpected arguments")
			}
		}}}
		raw := json.RawMessage(`{"_meta":null,"name":"empty"` + suffix + `}`)
		if _, err := h.CallTool(context.Background(), &jsonrpc.Request{Method: schema.MethodToolsCall, Params: raw}); err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatal("not dispatched")
		}
	}
}
