package server

import (
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server/auth"
	"net/http"
)

// Option is a function that configures the handler.
type Option func(s *Server) error

// WithCORS adds a new CORS handler to the handler.
func WithCORS(cors *Cors) Option {
	return func(s *Server) error {
		handler := &corsHandler{Cors: cors}
		s.corsHandler = handler.Middleware
		s.corsConfig = cors
		return nil
	}
}

func WithProtectedResourcesHandler(handler http.HandlerFunc) Option {
	return func(s *Server) error {
		s.protectedResourcesHandler = handler
		return nil
	}
}

// WithStreamableURI sets the base URI for the streamable HTTP endpoint (default "/mcp").
func WithStreamableURI(uri string) Option {
	return func(s *Server) error {
		s.streamableURI = uri
		return nil
	}
}

// WithSSEURI sets the base URI for the SSE endpoint (default "/sse").
func WithSSEURI(uri string) Option {
	return func(s *Server) error {
		s.sseURI = uri
		return nil
	}
}

// WithSSEMessageURI sets the message POST URI for SSE transport (default "/message").
func WithSSEMessageURI(uri string) Option {
	return func(s *Server) error {
		s.sseMessageURI = uri
		return nil
	}
}

// WithRootRedirect enables redirect from "/" to the active transport base URI.
func WithRootRedirect(enable bool) Option {
	return func(s *Server) error {
		s.rootRedirect = enable
		return nil
	}
}

// WithAuthorizer adds a new authorizer to the handler.
func WithAuthorizer(authorizer Middleware) Option {
	return func(s *Server) error {
		s.authorizer = authorizer
		return nil
	}
}

// WithJRPCAuthorizer adds a new JRPCAuthorizer to the handler.
func WithJRPCAuthorizer(authorizer auth.JRPCAuthorizer) Option {
	return func(s *Server) error {
		s.jRPCAuthorizer = authorizer
		return nil
	}
}

// WithImplementation sets the handler implementation.
func WithImplementation(implementation schema.Implementation) Option {
	return func(s *Server) error {
		s.info = implementation
		return nil
	}
}

// WithNewHandler sets the new handler.
func WithNewHandler(newHandler server.NewHandler) Option {
	return func(s *Server) error {
		s.newServer = newHandler
		return nil
	}
}

// WithLoggerName sets the logger name.
func WithLoggerName(name string) Option {
	return func(s *Server) error {
		s.loggerName = name
		return nil
	}
}

// WithEndpointAddress sets the protocol version.
func WithEndpointAddress(addr string) Option {
	return func(s *Server) error {
		s.httpServer.addr = addr
		return nil
	}
}

// WithCustomHTTPHandler adds a custom handler to the handler.
func WithCustomHTTPHandler(path string, handler http.HandlerFunc) Option {
	return func(s *Server) error {
		if s.customHTTPHandlers == nil {
			s.customHTTPHandlers = make(map[string]http.HandlerFunc)
		}
		s.customHTTPHandlers[path] = handler
		return nil
	}
}

// WithProtocolVersion sets the protocol version for the handler.
func WithProtocolVersion(version string) Option {
	return func(s *Server) error {
		s.protocolVersion = version
		return nil
	}
}
