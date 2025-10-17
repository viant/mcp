package server

import (
	"context"
	"net/http"

	"github.com/viant/jsonrpc/transport/server/http/sse"
	"github.com/viant/jsonrpc/transport/server/http/streamable"
)

type httpServer struct {
	sseHandler         *sse.Handler
	streamingHandler   *streamable.Handler
	useStreamableHTTP  bool
	addr               string
	customHTTPHandlers map[string]http.HandlerFunc
	sseURI             string
	sseMessageURI      string
	streamableURI      string
	rootRedirect       bool
}

// UseStreamableHTTP sets whether to use streamableHTTP or SSE for the HTTP handler.
func (s *Server) UseStreamableHTTP(flag bool) {
	s.useStreamableHTTP = flag
}

// HTTP creates and returns an HTTP handler with OAuth2 authorizer and SSE handlers.
func (s *Server) HTTP(_ context.Context, addr string) *http.Server {
	if addr == "" {
		addr = s.addr
	}
	if addr == "" {
		// Default bind only to localhost to reduce DNS rebinding risk
		addr = "127.0.0.1:5000"
	}
	// Defaults if not provided via options
	if s.sseURI == "" {
		s.sseURI = "/sse"
	}
	if s.sseMessageURI == "" {
		s.sseMessageURI = "/message"
	}
	if s.streamableURI == "" {
		s.streamableURI = "/mcp"
	}

	// SSE and Streamable handlers with configured URIs
	s.sseHandler = sse.New(s.NewHandler,
		sse.WithURI(s.sseURI),
		sse.WithMessageURI(s.sseMessageURI),
	)
	s.streamingHandler = streamable.New(s.NewHandler,
		streamable.WithURI(s.streamableURI),
	)
	mux := http.NewServeMux()
	if len(s.customHTTPHandlers) > 0 {
		for path, handler := range s.customHTTPHandlers {
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
	// Validate MCP-Protocol-Version and set response header
	middlewareHandlers = append(middlewareHandlers, protocolVersionMiddleware())
	middlewareHandlers = append(middlewareHandlers, s.corsHandler)
	// Validate Origin on all requests (uses configured CORS allowlist)
	if s.corsConfig != nil {
		middlewareHandlers = append(middlewareHandlers, originValidationMiddleware(s.corsConfig.AllowOrigins))
	}
	// Wrap handlers with middleware
	sseChain := ChainMiddlewareHandlers(s.sseHandler, middlewareHandlers...)
	streamChain := ChainMiddlewareHandlers(s.streamingHandler, middlewareHandlers...)

	// Mount handlers at their base URIs
	mux.Handle(s.sseURI, sseChain)
	mux.Handle(s.sseMessageURI, sseChain)
	mux.Handle(s.streamableURI, streamChain)

	// Optional root redirect to the active transport base
	if s.rootRedirect {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			target := s.sseURI
			if s.useStreamableHTTP {
				target = s.streamableURI
			}
			http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		})
	}
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return server
}
