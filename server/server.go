package server

import (
	"context"
	"errors"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp-protocol/syncmap"
	"github.com/viant/mcp/server/auth"
)

// Server represents MCP protocol handler
type Server struct {
	activeContexts *syncmap.Map[int, *activeContext]
	capabilities   schema.ServerCapabilities
	info           schema.Implementation
	newImplementer server.NewImplementer

	instructions    *string
	protocolVersion string
	loggerName      string
	meta            map[string]interface{}

	// auth handles OAuth2 authorization for incoming requests
	auth *auth.AuthServer

	//fine grained authorization
	authorizer auth.Authorizer

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
		authorizer: s.authorizer,
	}
	ret.Logger = NewLogger(ret.loggerName, &ret.loggingLevel, ret.Notifier)
	client := &Client{Transport: transport}
	ret.implementer, ret.err = s.newImplementer(ctx, transport, ret.Logger, client)
	return ret
}

// New creates a new Server instance
func New(options ...Option) (*Server, error) {
	// initialize server
	s := &Server{
		capabilities: schema.ServerCapabilities{},
		info: schema.Implementation{
			Name:    "MCP",
			Version: "0.1",
		},
		loggerName:      "server",
		protocolVersion: schema.LatestProtocolVersion,
		activeContexts:  syncmap.NewMap[int, *activeContext](),
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
