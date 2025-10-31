// Package namespace provides a reusable, policy-aware namespace resolution
// utility for MCP servers and services. It computes a stable, per-request
// namespace from the authorization token carried in the context, preferring
// identity claims (email/sub) when configured, and falling back to a token
// hash when identity is not available. It also derives filesystem-friendly
// path prefixes for namespaced storage and can inject a computed descriptor
// into context for downstream consumers.
package namespace
