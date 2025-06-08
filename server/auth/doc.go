// Package auth exposes helpers that make it easy to protect an MCP server with
// OAuth2/OIDC.
//
// It offers two complementary approaches:
//   - A strict global middleware (`AuthServer`) that validates bearer tokens for
//     every request except explicitly excluded URIs.
//   - A fallback wrapper (`FallbackAuth`) that automatically tries to obtain the
//     required token from a configurable token source and retries the protected
//     request on behalf of the caller.
//
// The package also contains support code for the experimental fine-grained
// per-JSON-RPC authorization mode.
package auth
