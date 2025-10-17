package server

import (
	"net/http"
)

// originValidationMiddleware enforces validation of the Origin header on all
// incoming requests. If the Origin header is present, it must match one of the
// allowed origins. A wildcard "*" allows any origin.
func originValidationMiddleware(allowed []string) Middleware {
	return func(next http.Handler) http.Handler {
		allowedMap := make(map[string]bool, len(allowed))
		for _, v := range allowed {
			allowedMap[v] = true
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Non-browser requests typically omit Origin; allow.
				next.ServeHTTP(w, r)
				return
			}
			// Wildcard permits all origins
			if allowedMap["*"] || allowedMap[origin] {
				next.ServeHTTP(w, r)
				return
			}
			// Reject unknown origins
			http.Error(w, "origin not allowed", http.StatusForbidden)
		})
	}
}
