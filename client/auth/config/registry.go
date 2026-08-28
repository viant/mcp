package config

import (
	"context"
	"fmt"
	"sync"
)

// StaticRegistry is an in-memory ProviderRegistry for hosts with a fixed
// provider set and for tests. Hosts with dynamic configuration implement
// ProviderRegistry directly.
type StaticRegistry struct {
	mu        sync.RWMutex
	providers map[string]*OAuthProvider
}

// NewStaticRegistry builds a registry from the supplied providers, keyed by
// provider ID. It fails on empty/duplicate IDs or invalid providers.
func NewStaticRegistry(providers ...*OAuthProvider) (*StaticRegistry, error) {
	registry := &StaticRegistry{providers: map[string]*OAuthProvider{}}
	for _, provider := range providers {
		if err := registry.Add(provider); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Add registers a provider.
func (r *StaticRegistry) Add(provider *OAuthProvider) error {
	if provider == nil {
		return fmt.Errorf("provider was nil")
	}
	if provider.ID == "" {
		return fmt.Errorf("provider id is required")
	}
	if err := provider.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[provider.ID]; ok {
		return fmt.Errorf("duplicate provider id %q", provider.ID)
	}
	r.providers[provider.ID] = provider
	return nil
}

// ResolveProvider implements ProviderRegistry.
func (r *StaticRegistry) ResolveProvider(_ context.Context, ref string) (*OAuthProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[ref]
	if !ok {
		return nil, fmt.Errorf("unknown oauth provider %q", ref)
	}
	return provider, nil
}

// MatchIssuer implements ProviderRegistry. It hard-fails when more than one
// provider shares the normalized issuer — ordering is never used as a
// tie-break.
func (r *StaticRegistry) MatchIssuer(_ context.Context, issuer string) (*OAuthProvider, error) {
	normalized := NormalizeIssuer(issuer)
	r.mu.RLock()
	defer r.mu.RUnlock()
	var matched *OAuthProvider
	for _, provider := range r.providers {
		if NormalizeIssuer(provider.Issuer) != normalized {
			continue
		}
		if matched != nil {
			return nil, fmt.Errorf("issuer %q matches multiple providers (%q, %q)", issuer, matched.ID, provider.ID)
		}
		matched = provider
	}
	if matched == nil {
		return nil, fmt.Errorf("no provider matches issuer %q", issuer)
	}
	return matched, nil
}
