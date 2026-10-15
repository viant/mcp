// SPDX-License-Identifier: Apache-2.0
// MCP client configuration extracted from Viant Agently Core.
package config

import (
	"testing"

	mcp "github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"
)

func delegatedClient(tokenType string, useIDToken bool) *MCPClient {
	return &MCPClient{
		ClientOptions: &mcp.ClientOptions{
			Name: "dev6",
			Auth: &mcp.ClientAuth{
				Mode:        authcfg.ModeOAuth,
				ProviderRef: "adelphic-dev6",
				TokenType:   tokenType,
				UseIdToken:  useIDToken,
			},
		},
	}
}

func TestNormalizeDelegatedAuth(t *testing.T) {
	// Legacy useIdToken=true with empty tokenType normalizes to idToken.
	legacy := delegatedClient("", true)
	if err := legacy.NormalizeDelegatedAuth(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if legacy.ClientOptions.Auth.TokenType != string(authcfg.TokenTypeIDToken) {
		t.Fatalf("tokenType = %q, want idToken", legacy.ClientOptions.Auth.TokenType)
	}

	// Explicit consistent tokenType stays as-is.
	consistent := delegatedClient(string(authcfg.TokenTypeIDToken), true)
	if err := consistent.NormalizeDelegatedAuth(); err != nil {
		t.Fatalf("consistent flags must pass: %v", err)
	}

	// Explicit conflicting tokenType fails validation.
	conflicting := delegatedClient(string(authcfg.TokenTypeAccessToken), true)
	if err := conflicting.NormalizeDelegatedAuth(); err == nil {
		t.Fatalf("useIdToken=true with tokenType=accessToken must fail")
	}

	// Without the legacy flag nothing changes.
	plain := delegatedClient("", false)
	if err := plain.NormalizeDelegatedAuth(); err != nil || plain.ClientOptions.Auth.TokenType != "" {
		t.Fatalf("tokenType must stay empty without useIdToken (err=%v)", err)
	}

	// Non-delegated configs keep legacy useIdToken behaviour untouched.
	legacyBFF := &MCPClient{
		ClientOptions: &mcp.ClientOptions{
			Auth: &mcp.ClientAuth{OAuth2ConfigURL: []string{"scy://legacy"}, UseIdToken: true},
		},
	}
	if err := legacyBFF.NormalizeDelegatedAuth(); err != nil {
		t.Fatalf("non-delegated config must pass: %v", err)
	}
	if legacyBFF.ClientOptions.Auth.TokenType != "" {
		t.Fatalf("non-delegated useIdToken must not set tokenType, got %q", legacyBFF.ClientOptions.Auth.TokenType)
	}
	var nilClient *MCPClient
	if err := nilClient.NormalizeDelegatedAuth(); err != nil {
		t.Fatalf("nil client must be a no-op: %v", err)
	}
}
