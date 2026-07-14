package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
)

func TestHTTP_ListTools_AllowsMissingParams(t *testing.T) {
	newHandler := serverproto.WithDefaultHandler(context.Background(), func(h *serverproto.DefaultHandler) error {
		type AddIn struct {
			A int `json:"a"`
			B int `json:"b"`
		}
		type AddOut struct {
			Sum int `json:"sum"`
		}
		return serverproto.RegisterTool[*AddIn, *AddOut](h.Registry, "add", "Add two integers", func(ctx context.Context, in *AddIn) (*schema.CallToolResult, *jsonrpc.Error) {
			payload, _ := json.Marshal(&AddOut{Sum: in.A + in.B})
			return &schema.CallToolResult{
				Content: []schema.CallToolResultContentElem{
					schema.TextContent{Text: string(payload), Type: "text"},
				},
			}, nil
		})
	})

	srv, err := New(
		WithNewHandler(newHandler),
		WithImplementation(schema.Implementation{Name: "test", Version: "1.0"}),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	httpSrv := srv.HTTP(context.Background(), ln.Addr().String())
	defer func() {
		_ = httpSrv.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = httpSrv.Shutdown(shutdownCtx)
		cancel()
	}()
	go func() { _ = httpSrv.Serve(ln) }()

	baseURL := "http://" + ln.Addr().String() + "/mcp"
	initBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`)
	initReq, _ := http.NewRequest(http.MethodPost, baseURL, bytes.NewReader(initBody))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initResp, err := http.DefaultClient.Do(initReq)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	defer initResp.Body.Close()
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		t.Fatalf("expected session id header")
	}
	_, _ = io.ReadAll(initResp.Body)

	toolsBody := []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	toolsReq, _ := http.NewRequest(http.MethodPost, baseURL, bytes.NewReader(toolsBody))
	toolsReq.Header.Set("Content-Type", "application/json")
	toolsReq.Header.Set("Accept", "application/json, text/event-stream")
	toolsReq.Header.Set("Mcp-Session-Id", sessionID)
	toolsResp, err := http.DefaultClient.Do(toolsReq)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}
	defer toolsResp.Body.Close()
	body, _ := io.ReadAll(toolsResp.Body)

	var response struct {
		Result *schema.ListToolsResult `json:"result"`
		Error  *jsonrpc.Error          `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("failed to unmarshal response: %v body=%s", err, string(body))
	}
	if response.Error != nil {
		t.Fatalf("expected no error, got %v body=%s", response.Error, string(body))
	}
	if response.Result == nil || len(response.Result.Tools) == 0 {
		t.Fatalf("expected at least one tool, got body=%s", string(body))
	}
}
