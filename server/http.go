package server

import (
	"context"
	"github.com/viant/jsonrpc/transport/server/http/sse"
	"github.com/viant/jsonrpc/transport/server/http/streaming"
	"net/http"
)

type httpServer struct {
	sseHandler       *sse.Handler
	streamingHandler *streaming.Handler
	useStreaming     bool
	addr             string
	customHandlers   map[string]http.HandlerFunc
}

// UseStreaming sets whether to use streaming or SSE for the HTTP server.
func (s *Server) UseStreaming(useStreaming bool) {
	s.useStreaming = useStreaming
}

// HTTP creates and returns an HTTP server with OAuth2 authorizer and SSE handlers.
func (s *Server) HTTP(_ context.Context, addr string) *http.Server {
	if addr == "" {
		addr = s.addr
	}
	if addr == "" {
		addr = ":5000" // Default address if not specified
	}
	// SSE handler for JSON-RPC transport
	s.sseHandler = sse.New(s.NewHandler)
	s.streamingHandler = streaming.New(s.NewHandler)
	mux := http.NewServeMux()
	if len(s.customHandlers) > 0 {
		for path, handler := range s.customHandlers {
			mux.Handle(path, handler)
		}
	}
	if s.protectedResourcesHandler != nil {
		mux.Handle("/.well-known/oauth-protected-resource", s.protectedResourcesHandler)
	}
	var middlewareHandlers []Middleware
	if s.authorizer != nil {
		middlewareHandlers = append(middlewareHandlers, s.authorizer)
	}
	var chain http.Handler
	middlewareHandlers = append(middlewareHandlers, s.corsHandler)
	if s.useStreaming {
		chain = ChainMiddlewareHandlers(s.streamingHandler, middlewareHandlers...)
	} else {
		chain = ChainMiddlewareHandlers(s.sseHandler, middlewareHandlers...)
	}
	mux.Handle("/", chain)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return server
}
