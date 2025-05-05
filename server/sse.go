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
// HTTP creates and returns an HTTP server with OAuth2 auth and SSE handlers.
func (s *Server) HTTP(_ context.Context, addr string) *http.Server {
	// SSE handler for JSON-RPC transport
	s.sseHandler = sse.New(s.NewHandler)

	mux := http.NewServeMux()
	// register OAuth2 endpoints
	if s.auth != nil {
		s.auth.RegisterHandlers(mux)
	}
	// protect all other endpoints (SSE) with authorization middleware
	if s.auth != nil {
		mux.Handle("/", s.auth.Middleware(s.sseHandler))
	} else {
		mux.Handle("/", s.sseHandler)
	}
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return server
}
