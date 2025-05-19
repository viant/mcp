package server

import (
	"context"
	"github.com/viant/jsonrpc/transport/server/http/sse"
	"net/http"
)

type sseServer struct {
	sseHandler *sse.Handler
}

// HTTP return http server
// HTTP creates and returns an HTTP server with OAuth2 authorizer and SSE handlers.
func (s *Server) HTTP(_ context.Context, addr string) *http.Server {
	// SSE handler for JSON-RPC transport
	s.sseHandler = sse.New(s.NewHandler)
	mux := http.NewServeMux()
	if s.protectedResourcesHandler != nil {
		mux.Handle("/.well-known/oauth-protected-resource", s.protectedResourcesHandler)
	}
	var middlewareHandlers []Middleware
	if s.authorizer != nil {
		middlewareHandlers = append(middlewareHandlers, s.authorizer)
	}
	middlewareHandlers = append(middlewareHandlers, s.corsHandler)
	chain := ChainMiddlewareHandlers(s.sseHandler, middlewareHandlers...)
	mux.Handle("/", chain)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return server
}
