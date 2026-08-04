// Package clientHandler implements a high-level Go clientHandler for the Model Context Protocol (MCP).
//
// It provides a thin wrapper around the protocol interface defined in the
// github.com/viant/mcp-protocol module and adds:
//   - Automatic `server/discover` negotiation for 2026-07-28, with legacy
//     `initialize` support when an older protocol is selected.
//   - Pluggable JSON-RPC transports (STDIO, HTTP/SSE, Streaming …).
//   - Optional authorization interceptor that can acquire OAuth2/OIDC tokens on the
//     fly and transparently retry failed requests.
//   - Convenience helpers such as strongly typed `ListResources`, `CallTool`,
//     `Complete`, … methods that avoid manual request/response handling.
//
// The package is transport-agnostic; callers supply any implementation that satisfies
// the jsonrpc/transport.Transport interface.
//
// Example:
//
//	sseTransport, _ := sse.New(ctx, "https://mcp.example.com/sse")
//	cli := clientHandler.New("demo", "1.0", sseTransport, clientHandler.WithCapabilities(schema.ClientCapabilities{}))
//	res, _ := cli.ListResources(ctx, nil)
//	fmt.Println(res.Resources)
package client
