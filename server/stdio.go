package server

import (
	"context"
	"github.com/viant/jsonrpc/transport/server/stdio"
)

type stdioServer struct {
	stdioServerOption []stdio.Option
}

// Stdio return stdio handler
func (s *Server) Stdio(ctx context.Context) *stdio.Server {
	return stdio.New(ctx, s.NewHandler, s.stdioServerOption...)
}
