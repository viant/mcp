package server

import (
	"github.com/viant/mcp-protocol/schema"
	"net/http"
)

// protocolVersionMiddleware validates MCP-Protocol-Version header per spec and
// sets the response header to the server's latest supported version.
func protocolVersionMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			version := r.Header.Get("MCP-Protocol-Version")
			if version != "" && version != schema.LatestProtocolVersion {
				http.Error(w, "invalid MCP-Protocol-Version", http.StatusBadRequest)
				return
			}
			// For absent version, fallback is assumed (per spec). Always set the server's version.
			w.Header().Set("MCP-Protocol-Version", schema.LatestProtocolVersion)
			next.ServeHTTP(w, r)
		})
	}
}
