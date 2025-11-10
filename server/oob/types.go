package oob

import (
	"time"
)

// Pending represents a typed pending interaction bound to a namespace.
// T holds service-specific payload (e.g., device-code message, form schema).
type Pending[T any] struct {
	ID        string
	Namespace string
	Kind      string // e.g., "device_code", "basic_credentials", "oauth_redirect", etc.
	Alias     string // optional account/connector alias
	Resource  string // provider domain/tenant/connector
	ElicitID  string // optional: if MCP Elicit was used to notify client

	CreatedAt time.Time
	ExpiresAt time.Time

	Data T // service-specific payload
}

// Spec carries inputs to create a Pending; Namespace is ignored on input
// and assigned from the caller context when creating the pending.
type Spec[T any] struct {
	Namespace string // ignored; filled from context at Create
	Kind      string
	Alias     string
	Resource  string
	ElicitID  string

	ExpiresAt time.Time // optional expiry time

	Data T
}
