package link

import (
	"reflect"
	"strings"
	"testing"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
)

func testStateConfig(active, previous string) *Config {
	return &Config{

		StateEncryptionKey:         active,
		StateEncryptionKeyPrevious: previous,
	}
}

func TestMCPStateKeyring_RoundTrip(t *testing.T) {
	keyring := newMCPStateKeyring(testStateConfig("active-key-material", ""))
	if keyring == nil {
		t.Fatalf("newMCPStateKeyring() = nil")
	}
	payload := &MCPAuthState{
		CanonicalUserID: "user-1",
		SessionIDHash:   keyring.mcpSessionHash("sess-1"),
		ServerName:      "dev6",
		ProviderRef:     "adelphic-dev6",
		Resource:        "https://mcp.test/mcp",
		Scopes:          []string{"plan:read"},
		CodeVerifier:    "verifier-value",
		Nonce:           "nonce-value",
		ExpiresAt:       time.Now().Add(7 * time.Minute).UTC(),
	}
	sealed, err := keyring.encryptMCPAuthState(payload)
	if err != nil {
		t.Fatalf("encryptMCPAuthState() error = %v", err)
	}
	if strings.Contains(sealed, "verifier-value") {
		t.Fatalf("state blob leaks the PKCE verifier")
	}
	decoded, err := keyring.decryptMCPAuthState(sealed)
	if err != nil {
		t.Fatalf("decryptMCPAuthState() error = %v", err)
	}
	if decoded.CodeVerifier != payload.CodeVerifier || decoded.CanonicalUserID != payload.CanonicalUserID || decoded.Nonce != payload.Nonce {
		t.Fatalf("decoded = %+v, want original payload", decoded)
	}
}

func TestMCPStateKeyring_TamperRejected(t *testing.T) {
	keyring := newMCPStateKeyring(testStateConfig("active-key-material", ""))
	sealed, err := keyring.encryptMCPAuthState(&MCPAuthState{CanonicalUserID: "user-1", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatalf("encryptMCPAuthState() error = %v", err)
	}
	tampered := sealed[:len(sealed)-2] + "aa"
	if tampered == sealed {
		tampered = sealed[:len(sealed)-2] + "bb"
	}
	if _, err := keyring.decryptMCPAuthState(tampered); err == nil {
		t.Fatalf("tampered state decrypted successfully; AEAD authentication failed to reject it")
	}
}

func TestMCPStateKeyring_RotationGrace(t *testing.T) {
	oldRing := newMCPStateKeyring(testStateConfig("old-key", ""))
	sealed, err := oldRing.encryptMCPAuthState(&MCPAuthState{CanonicalUserID: "user-1", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatalf("encryptMCPAuthState() error = %v", err)
	}
	// After rotation, state created before the rotation decrypts during the
	// grace window through the previous key.
	rotated := newMCPStateKeyring(testStateConfig("new-key", "old-key"))
	decoded, err := rotated.decryptMCPAuthState(sealed)
	if err != nil || decoded == nil || decoded.CanonicalUserID != "user-1" {
		t.Fatalf("rotated keyring failed to decrypt grace-window state: %+v, %v", decoded, err)
	}
	// Without the previous key the state must fail.
	withoutGrace := newMCPStateKeyring(testStateConfig("new-key", ""))
	if _, err := withoutGrace.decryptMCPAuthState(sealed); err == nil {
		t.Fatalf("state sealed with a retired key decrypted without the grace key")
	}
	// New state always seals with the active key: the old keyring cannot open it.
	newSealed, err := rotated.encryptMCPAuthState(&MCPAuthState{CanonicalUserID: "user-2", ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatalf("encryptMCPAuthState() error = %v", err)
	}
	if _, err := oldRing.decryptMCPAuthState(newSealed); err == nil {
		t.Fatalf("new state decrypted with only the retired key; active-key sealing is broken")
	}
}

func TestMCPStateKeyring_FallsBackToDelegatedSalt(t *testing.T) {
	cfg := &Config{TokenEncryptionKey: "delegated-salt"}
	if keyring := newMCPStateKeyring(cfg); keyring == nil {
		t.Fatalf("keyring should derive from the delegated token encryption salt")
	}
	if keyring := newMCPStateKeyring(&Config{}); keyring != nil {
		t.Fatalf("keyring without any key material must be nil (fail loud)")
	}
}

func TestMCPStateTTL_Clamped(t *testing.T) {
	if ttl := mcpStateTTL(&Config{}); ttl != mcpStateDefaultTTL {
		t.Fatalf("default TTL = %v, want %v", ttl, mcpStateDefaultTTL)
	}
	if ttl := mcpStateTTL(&Config{MCPLinkStateTTLMinutes: 1}); ttl != mcpStateMinTTL {
		t.Fatalf("low TTL = %v, want clamp to %v", ttl, mcpStateMinTTL)
	}
	if ttl := mcpStateTTL(&Config{MCPLinkStateTTLMinutes: 60}); ttl != mcpStateMaxTTL {
		t.Fatalf("high TTL = %v, want clamp to %v", ttl, mcpStateMaxTTL)
	}
	if ttl := mcpStateTTL(&Config{MCPLinkStateTTLMinutes: 6}); ttl != 6*time.Minute {
		t.Fatalf("in-window TTL = %v, want 6m", ttl)
	}
}

func TestMCPStateTTLForClient_UsesDedicatedAuthTimeout(t *testing.T) {
	client := &authcfg.OAuthClient{}
	field := reflect.ValueOf(client).Elem().FieldByName("AuthTimeout")
	if field.IsValid() && field.CanSet() {
		field.SetString("9m")
	}
	if ttl := mcpStateTTLForClient(&Config{MCPLinkStateTTLMinutes: 6}, client); ttl != 9*time.Minute {
		if field.IsValid() {
			t.Fatalf("client auth timeout = %v, want 9m", ttl)
		}
	}
	if ttl := mcpStateTTLForClient(&Config{}, &authcfg.OAuthClient{}); ttl != 5*time.Minute {
		t.Fatalf("default client auth timeout = %v, want 5m", ttl)
	}
}

func TestMCPStateKeyring_CSRFToken(t *testing.T) {
	keyring := newMCPStateKeyring(testStateConfig("active-key", "previous-key"))
	token := keyring.mcpCSRFToken("sess-1")
	if token == "" {
		t.Fatalf("mcpCSRFToken() = empty")
	}
	if !keyring.mcpCSRFTokenValid("sess-1", token) {
		t.Fatalf("valid CSRF token rejected")
	}
	if keyring.mcpCSRFTokenValid("sess-2", token) {
		t.Fatalf("CSRF token accepted for another session")
	}
	if keyring.mcpCSRFTokenValid("sess-1", "forged") {
		t.Fatalf("forged CSRF token accepted")
	}
	// Tokens minted before rotation stay valid during the grace window.
	previous := newMCPStateKeyring(testStateConfig("previous-key", ""))
	if !keyring.mcpCSRFTokenValid("sess-1", previous.mcpCSRFToken("sess-1")) {
		t.Fatalf("grace-window CSRF token rejected after rotation")
	}
}

func TestMCPFlowHash_NormalizesScopes(t *testing.T) {
	left := mcpFlowHash("user-1", "prov", "https://mcp.test/mcp", []string{"b", "a", "a"})
	right := mcpFlowHash("user-1", "prov", "https://mcp.test/mcp", []string{"a", "b"})
	if left != right {
		t.Fatalf("flow hash is not normalized over scope order/duplicates")
	}
	if left == mcpFlowHash("user-2", "prov", "https://mcp.test/mcp", []string{"a", "b"}) {
		t.Fatalf("flow hash ignores the canonical user")
	}
}

func TestSanitizeMCPReturnURL(t *testing.T) {
	cases := map[string]string{
		"/chat":                    "/chat",
		"":                         "",
		"//evil.test/x":            "",
		"https://evil.test/x":      "",
		"javascript:alert(1)":      "",
		"/ok?x=1":                  "/ok?x=1",
		"\\\\evil":                 "",
		"relative/without/slash":   "",
		"/nested/path#fragment-ok": "/nested/path#fragment-ok",
	}
	for input, want := range cases {
		if got := sanitizeMCPReturnURL(input); got != want {
			t.Fatalf("sanitizeMCPReturnURL(%q) = %q, want %q", input, got, want)
		}
	}
}
