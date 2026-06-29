package server

import (
	"net/http"
	"net/http/httptest"
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
		{name: "unknown version falls back to latest", requested: "2026-01-01", expect: schema.LatestProtocolVersion},
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
