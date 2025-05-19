package server

import (
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server/auth"
	"net/http"
)

// Option is a function that configures the server.
type Option func(s *Server) error

// WithCORS adds a new CORS handler to the server.
func WithCORS(cors *Cors) Option {
	return func(s *Server) error {
		handler := &corsHandler{Cors: cors}
		s.corsHandler = handler.Middleware
		return nil
	}
}

func WithProtectedResourcesHandler(handler http.HandlerFunc) Option {
	return func(s *Server) error {
		s.protectedResourcesHandler = handler
		return nil
	}
}

// WithAuthorizer adds a new authorizer to the server.
func WithAuthorizer(authorizer Middleware) Option {
	return func(s *Server) error {
		s.authorizer = authorizer
		return nil
	}
}

// WithJRPCAuthorizer adds a new JRPCAuthorizer to the server.
func WithJRPCAuthorizer(authorizer auth.JRPCAuthorizer) Option {
	return func(s *Server) error {
		s.jRPCAuthorizer = authorizer
		return nil
	}
}

// WithImplementation sets the server implementation.
func WithImplementation(implementation schema.Implementation) Option {
	return func(s *Server) error {
		s.info = implementation
		return nil
	}
}

// WithNewImplementer sets the new implementer.
func WithNewImplementer(newImplementer server.NewImplementer) Option {
	return func(s *Server) error {
		s.newImplementer = newImplementer
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
