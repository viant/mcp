// Package mcp provides high-level helpers for working with the Model Context Protocol (MCP).
//
// The package glues the low-level protocol types defined in the
// github.com/viant/mcp-protocol module with concrete transports, authentication layers
// and convenience configuration structures.  In practice it is used as an umbrella
// package that exposes two primary entry-points:
//  1. NewClient – returns a fully configured MCP client instance and
//  2. NewServer – returns a fully configured MCP server instance.
//
// Both constructors accept option structures that can be populated from CLI flags or
// configuration files, making it straightforward to spin up an MCP client/server with
// support for HTTP(SSE) or stdio transports, OAuth2 / "backend-for-frontend" flows and
// custom metadata.
//
// Example:
//
//	srv, _ := mcp.NewServer(myImplementation, &mcp.ServerOptions{ /* … */ })
//	cli, _ := mcp.NewClient(srv, &mcp.ClientOptions{ /* … */ })
//
// See the README for a more complete introduction.
package mcp
