package server

import (
	"github.com/viant/mcp-protocol/schema"
	"net/http"
	"strings"
)

// protocolVersionMiddleware sets the response MCP-Protocol-Version header.
// Negotiation should happen at initialize time; transport-level requests should
// not be rejected solely due to a newer client-advertised version.
func protocolVersionMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("MCP-Protocol-Version", negotiatedProtocolVersion(r.Header.Get("MCP-Protocol-Version")))
			next.ServeHTTP(w, r)
		})
	}
}

func negotiatedProtocolVersion(requested string) string {
	requested = strings.TrimSpace(requested)
	switch requested {
	case "", schema.LatestProtocolVersion:
		return schema.LatestProtocolVersion
	case "2025-06-18":
		return requested
	default:
		return schema.LatestProtocolVersion
	}
}
