// Package percall demonstrates per-call token usage. It starts a mock OAuth2
// authorization server and an MCP server that protects a tool. The client
// calls the tool by passing a bearer token via RequestOption on each call.
package percall
