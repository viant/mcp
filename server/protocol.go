package server

import (
	"github.com/viant/mcp-protocol/schema"
	"net/http"
)

// protocolVersionMiddleware sets the response MCP-Protocol-Version header.
// Negotiation should happen at initialize time; transport-level requests should
// not be rejected solely due to a newer client-advertised version.
func protocolVersionMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.Header.Get("MCP-Protocol-Version")
			w.Header().Set("MCP-Protocol-Version", schema.LatestProtocolVersion)
			next.ServeHTTP(w, r)
		})
	}
}
