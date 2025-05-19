package server

import "net/http"

// Middleware is a function that takes an http.Handler and returns an http.Handler
type Middleware func(next http.Handler) http.Handler

// ChainMiddlewareHandlers chains multiple middleware handlers together
func ChainMiddlewareHandlers(h http.Handler, mws ...Middleware) http.Handler {
	// apply in reverse so the first middleware is outermost
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
