package server

import (
	"context"
	"errors"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp-protocol/syncmap"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/server/auth"
	"net/http"
)

// Server represents MCP protocol handler
type Server struct {
	activeContexts            *syncmap.Map[int, *activeContext]
	info                      schema.Implementation
	newImplementer            server.NewImplementer
	instructions              *string
	protocolVersion           string
	loggerName                string
	protectedResourcesHandler http.HandlerFunc
	corsHandler               func(next http.Handler) http.Handler
	authorizer                func(next http.Handler) http.Handler
	jRPCAuthorizer            auth.JRPCAuthorizer
	stdioServer
	sseServer
}

func (s *Server) cancelOperation(id int) {
	if active, ok := s.activeContexts.Get(id); ok {
		active.CancelFunc()
		s.activeContexts.Delete(id)
	}
}

// NewHandler creates a new handler instance
func (s *Server) NewHandler(ctx context.Context, transport transport.Transport) transport.Handler {
	handler := s.newHandler(ctx, transport)
	return handler
}

func (s *Server) newHandler(ctx context.Context, transport transport.Transport) *Handler {
	ret := &Handler{
		Server:     s,
		Notifier:   transport,
		authorizer: s.jRPCAuthorizer,
	}
	ret.Logger = NewLogger(ret.loggerName, &ret.loggingLevel, ret.Notifier)
	aClient := &Client{Transport: transport}
	ret.implementer, ret.err = s.newImplementer(ctx, transport, ret.Logger, aClient)
	return ret
}

// AsClient returns a client.Interface implementation that uses this server directly
func (s *Server) AsClient(ctx context.Context) client.Interface {
	// Create a handler with a nil transport
	handler := s.newHandler(ctx, nil)
	return NewAdapter(handler)
}

// New creates a new Server instance
func New(options ...Option) (*Server, error) {
	corsHandler := &corsHandler{defaultCors()}
	// initialize server
	s := &Server{
		info: schema.Implementation{
			Name:    "MCP",
			Version: "0.1",
		},
		loggerName:      "server",
		protocolVersion: schema.LatestProtocolVersion,
		activeContexts:  syncmap.NewMap[int, *activeContext](),
		corsHandler:     corsHandler.Middleware,
	}
	for _, option := range options {
		if err := option(s); err != nil {
			return nil, err
		}
	}
	if s.newImplementer == nil {
		return nil, errors.New("no implementer specified")
	}
	return s, nil
}
