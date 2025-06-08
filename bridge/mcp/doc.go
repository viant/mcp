// Package mcp provides the implementation of the `bridge` proxy service.
//
// The proxy stands in between a local process and a remote MCP server, forwarding
// JSON-RPC requests and responses while transparently handling transport and
// authentication concerns.  It is used by the `bridge` command found one directory
// up, but can also be embedded programmatically if more control is required.
package mcp
