package server

import (
	"context"
	"errors"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp/internal/collection"
	"github.com/viant/mcp/protocol/server/auth"
	"github.com/viant/mcp/schema"
)

// Server represents MCP protocol handler
type Server struct {
	activeContexts  *collection.SyncMap[int, *activeContext]
	capabilities    schema.ServerCapabilities
	info            schema.Implementation
	newImplementer  NewImplementer
	instructions    *string
	protocolVersion string
	loggerName      string
	meta            map[string]interface{}
	// auth handles OAuth2 authorization for incoming requests
	auth *auth.AuthServer
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
	return s.newHandler(ctx, transport)
}

func (s *Server) newHandler(ctx context.Context, transport transport.Transport) *Handler {
	ret := &Handler{
		Server:   s,
		Notifier: transport,
	}
	ret.Logger = NewLogger(ret.loggerName, &ret.loggingLevel, ret.Notifier)
	client := &Client{Transport: transport}
	ret.implementer = s.newImplementer(ctx, transport, ret.Logger, client)
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
		activeContexts:  collection.NewSyncMap[int, *activeContext](),
	}
	for _, option := range options {
		option(s)
	}
	if s.newImplementer == nil {
		return nil, errors.New("no implementer specified")
	}
	return s, nil
}
