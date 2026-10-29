package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

type numericCompatInput struct {
	ID     int64                  `json:"id"`
	Values map[string]interface{} `json:"values"`
}

func TestNumericCompatibilityActualTypedHTTPBothProtocols(t *testing.T) {
	for _, version := range []string{schema.LegacyProtocolVersion, schema.LatestProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var calls atomic.Int64
			newHandler := serverproto.WithDefaultHandler(ctx, func(handler *serverproto.DefaultHandler) error {
				return serverproto.RegisterTool[*numericCompatInput, *numericCompatInput](handler.Registry, "numeric", "numeric", func(_ context.Context, input *numericCompatInput) (*schema.CallToolResult, *jsonrpc.Error) {
					calls.Add(1)
					if input.ID < 1 || input.ID > 4 || input.Values["large"] != float64(9007199254740992) || input.Values["small"].(float64) != 7 || input.Values["fraction"].(float64) != 0.1 || input.Values["exponent"].(float64) != 1000 || input.Values["null"] != nil {
						return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("legacy input changed: %+v", input), nil)
					}
					return &schema.CallToolResult{Content: []schema.CallToolResultContentElem{}, ResultType: schema.ResultTypeComplete, StructuredContent: input}, nil
				})
			})
			server, err := mcpserver.New(mcpserver.WithNewHandler(newHandler), mcpserver.WithImplementation(schema.Implementation{Name: "numeric", Version: "1"}))
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			httpServer := server.HTTP(ctx, listener.Addr().String())
			go func() { _ = httpServer.Serve(listener) }()
			defer httpServer.Close()
			native, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, &ClientOptions{Name: "numeric", Version: "1", ProtocolVersion: version, Transport: ClientTransport{Type: "streamable", ClientTransportHTTP: ClientTransportHTTP{URL: "http://" + listener.Addr().String() + "/mcp"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer native.Close()
			failures := make(chan error, 4)
			var group sync.WaitGroup
			for _, id := range []int64{1, 2, 3, 4} {
				group.Add(1)
				go func() {
					defer group.Done()
					args := map[string]interface{}{"id": id, "values": map[string]interface{}{"large": json.Number("9007199254740993"), "maxUint64": json.Number("18446744073709551615"), "precise": json.Number("0.12345678901234567890123456789"), "small": 7, "fraction": 0.1, "exponent": json.Number("1e3"), "null": nil}, "unknown": "ignored"}
					result, err := native.CallTool(ctx, &schema.CallToolRequestParams{Name: "numeric", Arguments: args})
					if err != nil {
						failures <- err
						return
					}
					object := result.StructuredContent.(map[string]interface{})
					values := object["values"].(map[string]interface{})
					if object["id"].(float64) != float64(id) || values["large"].(float64) != 9007199254740992 || values["maxUint64"].(float64) != float64(18446744073709551615.0) || values["precise"].(float64) != 0.12345678901234568 || values["small"].(float64) != 7 || values["fraction"].(float64) != 0.1 || values["exponent"].(float64) != 1000 || values["null"] != nil {
						failures <- fmt.Errorf("response/correlation changed for %d: %#v", id, object)
					}
				}()
			}
			group.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
			}
			beforeOverflow := calls.Load()
			if _, err := native.CallTool(ctx, &schema.CallToolRequestParams{Name: "numeric", Arguments: map[string]interface{}{"id": 1, "values": map[string]interface{}{"large": json.Number("1e400")}}}); err == nil {
				t.Fatal("overflow accepted")
			}
			if calls.Load() != beforeOverflow {
				t.Fatal("overflow reached tool handler")
			}
			canceled, stop := context.WithCancel(ctx)
			stop()
			if _, err := native.CallTool(canceled, &schema.CallToolRequestParams{Name: "numeric"}); err == nil {
				t.Fatal("cancellation ignored")
			}
			if _, err := native.Ping(ctx, &schema.PingRequestParams{}); err != nil {
				t.Fatalf("cancellation poisoned connection: %v", err)
			}
		})
	}
}
