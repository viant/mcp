package server

import (
	"context"
	"github.com/viant/jsonrpc/transport/server/stdio"
)

type stdioServer struct {
	stdioServerOption []stdio.Option
}

// Stdio return stdio server
func (s *Server) Stdio(ctx context.Context) *stdio.Server {
	return stdio.New(ctx, s.NewHandler, s.stdioServerOption...)
}

/*
-32001 — no token provided, but the server expected one.
-32003 — token present but invalid/insufficient (expired, missing scope, etc).
*/
