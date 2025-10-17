// Package mcp provides the implementation of the `bridge` proxy service.
//
// The proxy stands in between a local process and a remote MCP server, forwarding
// JSON-RPC requests and responses while transparently handling transport and
// authentication concerns.  It is used by the `bridge` command found one directory
// up, but can also be embedded programmatically if more control is required.
package mcp

// Transport selection
//
// The bridge autodetects the downstream transport of the remote MCP server:
// it first attempts a Streamable HTTP initialize (single-endpoint /mcp). If that
// succeeds, it uses the Streamable client; otherwise it falls back to the SSE
// client (/sse + /message). OAuth2 / backend-for-frontend authentication, when
// configured, is applied consistently to either transport.
