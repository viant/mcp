package server

import (
	"context"
	"github.com/viant/jsonrpc/transport/server/http/sse"
	"net/http"
)

type sseServer struct {
	sseHandler *sse.Handler
}

// HTTP return http server
func (s *Server) HTTP(ctx context.Context, addr string) *http.Server {
	s.sseHandler = sse.New(s.NewHandler)
	server := http.Server{
		Addr:    addr,
		Handler: s.sseHandler,
	}
	return &server
}
