package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStaticRegistry(t *testing.T) {
	ctx := context.Background()
	registry, err := NewStaticRegistry(
		&OAuthProvider{ID: "dev6", Issuer: "https://idp-dev6.example.com/"},
		&OAuthProvider{ID: "dev7", Issuer: "https://idp-dev7.example.com"},
	)
	assert.NoError(t, err)

	provider, err := registry.ResolveProvider(ctx, "dev6")
	assert.NoError(t, err)
	assert.Equal(t, "dev6", provider.ID)

	_, err = registry.ResolveProvider(ctx, "unknown")
	assert.Error(t, err)

	// issuer matching is exact after normalization (trailing slash trimmed)
	provider, err = registry.MatchIssuer(ctx, "https://idp-dev6.example.com")
	assert.NoError(t, err)
	assert.Equal(t, "dev6", provider.ID)

	_, err = registry.MatchIssuer(ctx, "https://unknown.example.com")
	assert.Error(t, err)

	// no prefix matching: a longer path must not match
	_, err = registry.MatchIssuer(ctx, "https://idp-dev6.example.com/tenant")
	assert.Error(t, err)
}

func TestStaticRegistry_DuplicateIssuerHardFails(t *testing.T) {
	registry, err := NewStaticRegistry(
		&OAuthProvider{ID: "a", Issuer: "https://idp.example.com/"},
		&OAuthProvider{ID: "b", Issuer: "https://idp.example.com"},
	)
	assert.NoError(t, err)
	_, err = registry.MatchIssuer(context.Background(), "https://idp.example.com")
	assert.Error(t, err, "ambiguous issuer must hard-fail, never tie-break by order")
}

func TestStaticRegistry_Add(t *testing.T) {
	registry, err := NewStaticRegistry()
	assert.NoError(t, err)
	assert.Error(t, registry.Add(nil))
	assert.Error(t, registry.Add(&OAuthProvider{Issuer: "https://idp.example.com"}), "id required")
	assert.Error(t, registry.Add(&OAuthProvider{ID: "x"}), "issuer required")
	assert.NoError(t, registry.Add(&OAuthProvider{ID: "x", Issuer: "https://idp.example.com"}))
	assert.Error(t, registry.Add(&OAuthProvider{ID: "x", Issuer: "https://other.example.com"}), "duplicate id")
}
