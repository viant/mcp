package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

type numericCompatInput struct {
	ID           int64       `json:"id"`
	Max          uint64      `json:"max"`
	Decimal      json.Number `json:"decimal"`
	Exponent     json.Number `json:"exponent"`
	Small        float64     `json:"small"`
	Fraction     float64     `json:"fraction"`
	SafeExponent float64     `json:"safeExponent"`
	Null         *string     `json:"null"`
}

func TestNumericCompatibilityActualTypedHTTPBothProtocols(t *testing.T) {
	for _, version := range []string{schema.LegacyProtocolVersion, schema.LatestProtocolVersion} {
		t.Run(version, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			newHandler := serverproto.WithDefaultHandler(ctx, func(handler *serverproto.DefaultHandler) error {
				return serverproto.RegisterTool[*numericCompatInput, *numericCompatInput](handler.Registry, "numeric", "numeric", func(_ context.Context, input *numericCompatInput) (*schema.CallToolResult, *jsonrpc.Error) {
					if input.Max != ^uint64(0) || input.Decimal != "0.12345678901234567890123456789" || input.Exponent != "1.234567890123456789e100" || input.Small != 7 || input.Fraction != 0.1 || input.SafeExponent != 1000 || input.Null != nil {
						return nil, jsonrpc.NewInvalidParamsError(fmt.Sprintf("typed input changed: %+v", input), nil)
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
			failures := make(chan error, 5)
			var group sync.WaitGroup
			for _, id := range []string{"9007199254740993", "9007199254740995", "1000000000000000100", "9223372036854775807", "-9223372036854775808"} {
				group.Add(1)
				go func() {
					defer group.Done()
					args := map[string]interface{}{"id": json.Number(id), "max": json.Number("18446744073709551615"), "decimal": json.Number("0.12345678901234567890123456789"), "exponent": json.Number("1.234567890123456789e100"), "small": 7, "fraction": 0.1, "safeExponent": json.Number("1e3"), "null": nil, "unknown": json.Number("9007199254740997")}
					result, err := native.CallTool(ctx, &schema.CallToolRequestParams{Name: "numeric", Arguments: args})
					if err != nil {
						failures <- err
						return
					}
					values := result.StructuredContent.(map[string]interface{})
					if values["id"] != json.Number(id) || values["max"] != json.Number("18446744073709551615") || values["decimal"] != json.Number("0.12345678901234567890123456789") || values["exponent"] != json.Number("1.234567890123456789e100") || values["small"].(float64) != 7 || values["fraction"].(float64) != 0.1 || values["safeExponent"].(float64) != 1000 || values["null"] != nil {
						failures <- fmt.Errorf("response/correlation changed for %s: %#v", id, values)
						return
					}
					encoded, err := json.Marshal(result)
					if err != nil {
						failures <- err
						return
					}
					var raw struct {
						Structured struct {
							ID  json.Number `json:"id"`
							Max json.Number `json:"max"`
						} `json:"structuredContent"`
					}
					if err = json.Unmarshal(encoded, &raw); err != nil || raw.Structured.ID.String() != id || raw.Structured.Max != "18446744073709551615" {
						failures <- fmt.Errorf("wire changed: %s %v", encoded, err)
					}
				}()
			}
			group.Wait()
			close(failures)
			for err := range failures {
				t.Error(err)
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
