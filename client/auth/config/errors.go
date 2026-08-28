package config

import (
	"errors"
	"fmt"
	"strings"
)

// OAuthLinkRequiredError signals that no usable credential exists (or can be
// refreshed) for a requirement and interactive (re-)linking through the host
// is needed. It is the terminal, typed outcome of the transport-level 401
// recovery sequence (one refresh, one retry).
type OAuthLinkRequiredError struct {
	ServerName  string
	ProviderRef string
	Issuer      string
	Resource    string
	Scopes      []string
	// MetadataURL carries the protected-resource metadata URL the requirement
	// was enriched from (challenge-mode provider learning), so hosts can
	// persist the learned issuer/resource binding.
	MetadataURL string
	// Cause preserves the underlying failure (e.g. refresh rejection).
	Cause error
}

// Error implements error.
func (e *OAuthLinkRequiredError) Error() string {
	var b strings.Builder
	b.WriteString("oauth link required")
	if e.ServerName != "" {
		fmt.Fprintf(&b, " for server %q", e.ServerName)
	}
	if e.ProviderRef != "" {
		fmt.Fprintf(&b, " provider %q", e.ProviderRef)
	}
	if e.Resource != "" {
		fmt.Fprintf(&b, " resource %q", e.Resource)
	}
	if e.Cause != nil {
		fmt.Fprintf(&b, ": %v", e.Cause)
	}
	return b.String()
}

// Unwrap exposes the underlying cause for errors.Is/As inspection.
func (e *OAuthLinkRequiredError) Unwrap() error { return e.Cause }

// NewLinkRequired builds an OAuthLinkRequiredError from a requirement,
// preserving cause. When cause is already an *OAuthLinkRequiredError it is
// returned unchanged so resolver-produced details survive.
func NewLinkRequired(requirement *Requirement, cause error) *OAuthLinkRequiredError {
	var linkRequired *OAuthLinkRequiredError
	if errors.As(cause, &linkRequired) {
		return linkRequired
	}
	result := &OAuthLinkRequiredError{Cause: cause}
	if requirement != nil {
		result.ServerName = requirement.ServerName
		result.ProviderRef = requirement.ProviderRef
		result.Issuer = requirement.Issuer
		result.Resource = requirement.Resource
		result.Scopes = requirement.Scopes
		result.MetadataURL = requirement.MetadataURL
	}
	return result
}

// IsLinkRequired reports whether err (or any error it wraps) is an
// OAuthLinkRequiredError.
func IsLinkRequired(err error) bool {
	var linkRequired *OAuthLinkRequiredError
	return errors.As(err, &linkRequired)
}
