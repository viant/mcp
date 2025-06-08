// Package server provides a configurable MCP server implementation.
//
// It wires protocol handlers from the github.com/viant/mcp-protocol/server
// package with optional middleware such as:
//   - Transport (HTTP, HTTP-SSE, Streaming, STDIO)
//   - OAuth2 / OIDC authorization
//   - CORS handling
//   - Structured logging
//
// Callers typically construct a server via `server.New` and then expose it over
// HTTP or stdio:
//
//	s, _ := server.New(server.WithNewServer(myImpl))
//	log.Fatal(s.HTTP(ctx, ":4981").ListenAndServe())
package server
