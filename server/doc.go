// Package handler provides a configurable MCP handler implementation.
//
// It wires protocol handlers from the github.com/viant/mcp-protocol/handler
// package with optional middleware such as:
//   - Transport (HTTP, HTTP-SSE, Streaming, STDIO)
//   - OAuth2 / OIDC authorization
//   - CORS handling
//   - Structured logging
//
// Callers typically construct a handler via `server.New` and then expose it over
// HTTP or stdio:
//
//	s, _ := handler.New(handler.WithNewHandler(myImpl))
//	log.Fatal(s.HTTP(ctx, ":4981").ListenAndServe())
package server
