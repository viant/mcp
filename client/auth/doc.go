// Package auth contains supporting helpers that enable fine-grained client side
// authorization when talking to an MCP server.
//
// The Authorizer type can be attached to a client via `client.WithAuthInterceptor`.
// When a request is rejected with a *401 Unauthorized* response it automatically
// discovers the protected-resource metadata, obtains the necessary OAuth 2.1 token
// using the RoundTripper from the `transport` sub-package, and transparently retries
// the call.
package auth
