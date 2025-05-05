package server

import (
	"fmt"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server/auth"
)

// Option is a function that configures the server.
type Option func(s *Server) error

// WithCapabilities sets the server capabilities.
func WithCapabilities(capabilities schema.ServerCapabilities) Option {
	return func(s *Server) error {
		s.capabilities = capabilities
		return nil
	}
}

// WithAuthConfig accepts authentication configuration (no-op stub).
func WithAuthConfig(config *authorization.Config) Option {
	return func(s *Server) (err error) {
		if s.auth, err = auth.NewAuthServer(config); err != nil {
			return fmt.Errorf("unable to create auth server %v: %v", config, err)
		}
		if s.auth.Config.IsFineGrained() && s.authorizer == nil {
			s.authorizer = s.auth.EnsureAuthorized
		}
		return nil
	}
}

func WithAuthorizer(authorizer auth.Authorizer) Option {
	return func(s *Server) error {
		s.authorizer = authorizer
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
