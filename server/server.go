package server

import (
	"context"
	"errors"
	"github.com/viant/jsonrpc/transport"
	streamauth "github.com/viant/jsonrpc/transport/server/auth"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp-protocol/syncmap"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/server/auth"
	"net/http"
	"sync"
)

// Server represents MCP protocol handler
type Server struct {
	maxRequestBodyBytes       int64
	activeContexts            *syncmap.Map[int, *activeContext]
	activeContextsMu          sync.Mutex
	info                      schema.Implementation
	newServer                 server.NewHandler
	instructions              *string
	protocolVersion           string
	loggerName                string
	toolProtocolErrors        bool
	protectedResourcesHandler http.HandlerFunc
	corsHandler               func(next http.Handler) http.Handler
	corsConfig                *Cors
	authorizer                func(next http.Handler) http.Handler
	jRPCAuthorizer            auth.JRPCAuthorizer
	bffAuthStore              streamauth.Store
	requestContext            func(context.Context) (context.Context, error)
	onSessionClose            func(transport.Notifier)
	stdioServer
	httpServer
}

func (s *Server) CancelOperation(id int) {
	s.activeContextsMu.Lock()
	defer s.activeContextsMu.Unlock()
	if active, ok := s.activeContexts.Get(id); ok {
		active.CancelFunc()
		s.activeContexts.Delete(id)
	}
}

func (s *Server) registerOperation(id int, active *activeContext) {
	s.activeContextsMu.Lock()
	defer s.activeContextsMu.Unlock()
	s.activeContexts.Put(id, active)
}

// Completion belongs to the request, even when another transport uses the same
// numeric request ID. Never cancel or remove that other request's registration.
func (s *Server) completeOperation(id int, active *activeContext) {
	active.CancelFunc()
	s.activeContextsMu.Lock()
	defer s.activeContextsMu.Unlock()
	if current, ok := s.activeContexts.Get(id); ok && current == active {
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
		Server:             s,
		Notifier:           transport,
		authorizer:         s.jRPCAuthorizer,
		clientFeatures:     make(map[string]bool),
		toolProtocolErrors: s.toolProtocolErrors,
	}
	if transport != nil {
		ret.subscriptions = &subscriptionNotifications{raw: transport}
		ret.Notifier = ret.subscriptions
	}
	ret.Logger = NewLogger(ret.loggerName, &ret.loggingLevel, ret.Notifier)

	aClient := NewClient(ret.clientFeatures, transport)
	ret.handler, ret.err = s.newServer(ctx, ret.Notifier, ret.Logger, aClient)
	return ret
}

// AsClient returns a client.Interface implementation that uses this handler directly
func (s *Server) AsClient(ctx context.Context) client.Interface {
	// Create a handler with a nil transport
	handler := s.newHandler(ctx, nil)
	return NewAdapter(handler)
}

// New creates a new Server instance
func New(options ...Option) (*Server, error) {
	dCors := defaultCors()
	corsHandler := &corsHandler{dCors}
	// initialize handler
	s := &Server{
		info: schema.Implementation{
			Name:    "MCP",
			Version: "0.1",
		},
		loggerName:      "handler",
		protocolVersion: schema.LatestProtocolVersion,
		activeContexts:  syncmap.NewMap[int, *activeContext](),
		corsHandler:     corsHandler.Middleware,
		corsConfig:      dCors,
	}
	for _, option := range options {
		if err := option(s); err != nil {
			return nil, err
		}
	}

	if s.newServer == nil {
		return nil, errors.New("no handler specified")
	}
	return s, nil
}
