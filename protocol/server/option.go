package server

import "github.com/viant/mcp/schema"

// Option is a function that configures the server.
type Option func(s *Server)

// WithCapabilities sets the server capabilities.
func WithCapabilities(capabilities schema.ServerCapabilities) Option {
	return func(s *Server) {
		s.capabilities = capabilities
	}
}

// WithImplementation sets the server implementation.
func WithImplementation(implementation schema.Implementation) Option {
	return func(s *Server) {
		s.info = implementation
	}
}

// WithNewImplementer sets the new implementer.
func WithNewImplementer(newImplementer NewImplementer) Option {
	return func(s *Server) {
		s.newImplementer = newImplementer
	}
}

// WithLoggerName sets the logger name.
func WithLoggerName(name string) Option {
	return func(s *Server) {
		s.loggerName = name
	}
}
