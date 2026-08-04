package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/viant/mcp-protocol/schema"
)

func TestNegotiatedProtocolVersion(t *testing.T) {
	testCases := []struct {
		name      string
		requested string
		expect    string
	}{
		{name: "empty defaults to latest", expect: schema.LatestProtocolVersion},
		{name: "latest preserved", requested: schema.LatestProtocolVersion, expect: schema.LatestProtocolVersion},
		{name: "older supported version preserved", requested: "2025-06-18", expect: "2025-06-18"},
		{name: "legacy preserved", requested: schema.LegacyProtocolVersion, expect: schema.LegacyProtocolVersion},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := negotiatedProtocolVersion(testCase.requested)
			if actual != testCase.expect {
				t.Fatalf("expected %q, got %q", testCase.expect, actual)
			}
		})
	}
}

func TestProtocolVersionMiddleware_ValidatesJulyStandardHeaders(t *testing.T) {
	handler := protocolVersionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"weather","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set(schema.HeaderProtocolVersion, schema.LatestProtocolVersion)
	request.Header.Set(schema.HeaderMethod, schema.MethodToolsCall)
	request.Header.Set(schema.HeaderName, "weather")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d: %s", http.StatusNoContent, response.Code, response.Body.String())
	}
}

func TestProtocolVersionMiddleware_RejectsJulyHeaderMismatch(t *testing.T) {
	handler := protocolVersionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("mismatched request reached handler")
	}))
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"weather","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set(schema.HeaderProtocolVersion, schema.LatestProtocolVersion)
	request.Header.Set(schema.HeaderMethod, schema.MethodToolsCall)
	request.Header.Set(schema.HeaderName, "calendar")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, response.Code)
	}
	var result struct {
		ID    int `json:"id"`
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != 7 || result.Error.Code != schema.ErrorCodeHeaderMismatch {
		t.Fatalf("unexpected error response: %s", response.Body.String())
	}
}

func TestProtocolVersionMiddleware_RequiresJulyBodyVersion(t *testing.T) {
	handler := protocolVersionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("invalid request reached handler")
	}))
	body := `{"jsonrpc":"2.0","id":8,"method":"tools/list","params":{"_meta":{}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set(schema.HeaderProtocolVersion, schema.LatestProtocolVersion)
	request.Header.Set(schema.HeaderMethod, schema.MethodToolsList)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, response.Code)
	}
}

func TestProtocolVersionMiddleware_UsesNegotiatedVersion(t *testing.T) {
	handler := protocolVersionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("MCP-Protocol-Version", "2025-06-18")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if actual := response.Header().Get("MCP-Protocol-Version"); actual != "2025-06-18" {
		t.Fatalf("expected negotiated protocol header %q, got %q", "2025-06-18", actual)
	}
}

func TestProtocolVersionMiddleware_RejectsUnsupportedVersion(t *testing.T) {
	handler := protocolVersionMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unsupported request reached handler")
	}))
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("MCP-Protocol-Version", "2026-01-01")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, response.Code)
	}
}
