// Package transport implements an http.RoundTripper that performs the OAuth 2.1
// [Protected Resource Metadata](https://www.rfc-editor.org/rfc/rfc9728) discovery,
// token acquisition and automatic request retry logic required by MCP when a
// server challenges the client with `401 Unauthorized`.
//
// The RoundTripper integrates seamlessly with the higher-level `auth.Authorizer`
// interceptor but can also be used directly to secure arbitrary HTTP traffic.
package transport
