package link

import (
	"strings"
	"testing"
)

func TestDelegatedProviderStorageKeyShape(t *testing.T) {
	key := DelegatedProviderStorageKey("prod-workspace", "adelphic-dev6")
	if !strings.HasPrefix(key, DelegatedProviderKeyPrefix) {
		t.Fatalf("key %q missing prefix", key)
	}
	if len(key) != 71 {
		t.Fatalf("storage key must be exactly 71 ASCII characters, got %d", len(key))
	}
	if len(key) != DelegatedProviderKeyLength {
		t.Fatalf("DelegatedProviderKeyLength mismatch: %d vs %d", len(key), DelegatedProviderKeyLength)
	}
	// Deterministic: same inputs, same key.
	if DelegatedProviderStorageKey("prod-workspace", "adelphic-dev6") != key {
		t.Fatalf("storage key derivation must be deterministic")
	}
	// Namespace and providerRef both discriminate.
	if DelegatedProviderStorageKey("other-workspace", "adelphic-dev6") == key {
		t.Fatalf("namespace must discriminate storage keys")
	}
	if DelegatedProviderStorageKey("prod-workspace", "other-provider") == key {
		t.Fatalf("providerRef must discriminate storage keys")
	}
	// NUL separator prevents boundary ambiguity.
	if DelegatedProviderStorageKey("ab", "c") == DelegatedProviderStorageKey("a", "bc") {
		t.Fatalf("namespace/providerRef boundary must be unambiguous")
	}
}

func TestIsDelegatedProviderKey(t *testing.T) {
	key := DelegatedProviderStorageKey("ns", "ref")
	if !IsDelegatedProviderKey(key) {
		t.Fatalf("derived key must classify as delegated")
	}
	for _, invalid := range []string{"", "oauth", "jwt", "mcp:v1:short", key + "x"} {
		if IsDelegatedProviderKey(invalid) {
			t.Fatalf("%q must not classify as delegated", invalid)
		}
	}
}

func TestCanonicalWorkspaceProvider(t *testing.T) {
	cfg := &Config{OAuth: &OAuth{Name: "corp-idp"}}
	for _, alias := range []string{"", "jwt", "oauth", "default", "OAUTH", "corp-idp"} {
		if got := CanonicalWorkspaceProvider(cfg, alias); got != "corp-idp" {
			t.Fatalf("alias %q normalized to %q, want corp-idp", alias, got)
		}
	}
	// Unknown provider names pass through unchanged: jwt/oauth never fall
	// through to an arbitrary provider row.
	if got := CanonicalWorkspaceProvider(cfg, "github"); got != "github" {
		t.Fatalf("unknown provider %q must pass through", got)
	}
	// Delegated keys are never normalized.
	key := DelegatedProviderStorageKey("ns", "ref")
	if got := CanonicalWorkspaceProvider(cfg, key); got != key {
		t.Fatalf("delegated key must pass through unchanged")
	}
	// No configured OAuth name: aliases normalize to "oauth".
	if got := CanonicalWorkspaceProvider(nil, "jwt"); got != "oauth" {
		t.Fatalf("alias without config normalized to %q, want oauth", got)
	}
}

func TestIsWorkspaceProviderAlias(t *testing.T) {
	cfg := &Config{OAuth: &OAuth{Name: "corp-idp"}}
	for _, alias := range []string{"", "jwt", "oauth", "local", "corp-idp"} {
		if !IsWorkspaceProviderAlias(cfg, alias) {
			t.Fatalf("%q must be a workspace alias", alias)
		}
	}
	if IsWorkspaceProviderAlias(cfg, "github") {
		t.Fatalf("unknown provider must not be a workspace alias")
	}
	if IsWorkspaceProviderAlias(cfg, DelegatedProviderStorageKey("ns", "ref")) {
		t.Fatalf("delegated key must not be a workspace alias")
	}
}
