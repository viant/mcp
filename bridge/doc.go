// Command bridge is a standalone binary that runs an MCP bridge.
//
// The bridge acts as a local proxy between an MCP compatible client or tool and a
// remote MCP server.  It allows existing CLI tools to be exposed to the server (or
// vice-versa) without having to embed the Go libraries directly.  Most end-users will
// interact with the compiled `mcpb` binary that is distributed in the release
// artifacts, but the source lives in this directory.
package main
