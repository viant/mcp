package link

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
)

// MCP OAuth link state is protected with authenticated encryption (AES-256-GCM)
// plus a keyring holding the active key and the previous key for at least the
// state TTL plus clock skew, so key rotation never invalidates in-flight
// authorization flows. Encryption alone does not make state single-use — the
// distributed OAuthStateStore enforces that.

const (
	// mcpStateVersion prefixes every encrypted state blob; unknown versions
	// fail closed.
	mcpStateVersion = "v1"
	// mcpStateAAD binds ciphertexts to this purpose so blobs cannot be
	// replayed into other AEAD surfaces sharing key material.
	mcpStateAAD = "agently:mcp-oauth-state:v1"

	// State TTL window mandated by the design: default 7 minutes, clamped to
	// [5, 10] minutes when configured explicitly.
	mcpStateDefaultTTL = 5 * time.Minute
	mcpStateMinTTL     = 5 * time.Minute
	mcpStateMaxTTL     = 10 * time.Minute
)

// MCPAuthState is the encrypted callback state payload for delegated MCP
// OAuth. It round-trips through the browser only as AEAD ciphertext; the
// database stores only its hash. The PKCE verifier lives exclusively here.
type MCPAuthState struct {
	CanonicalUserID string    `json:"canonicalUserId"`
	SessionIDHash   string    `json:"sessionIdHash"`
	ServerName      string    `json:"serverName"`
	ProviderRef     string    `json:"providerRef"`
	ClientRef       string    `json:"clientRef,omitempty"`
	Resource        string    `json:"resource,omitempty"`
	Scopes          []string  `json:"scopes,omitempty"`
	CodeVerifier    string    `json:"codeVerifier"`
	ReturnURL       string    `json:"returnURL,omitempty"`
	Nonce           string    `json:"nonce"`
	ExpiresAt       time.Time `json:"expiresAt"`
	// ConfigFingerprint records the non-secret provider-registry fingerprint
	// at initiation; the callback verifies the configuration has not changed
	// underneath the flow.
	ConfigFingerprint string `json:"configFingerprint,omitempty"`
	// RedirectURI pins the exact redirect used on the authorization request so
	// the token exchange sends the identical value.
	RedirectURI string `json:"redirectURI,omitempty"`
}

// mcpStateKeyring derives fixed 32-byte AES-256-GCM keys from the configured
// secrets. New state always seals with the active key; retired keys decrypt
// during the rotation grace window only.
type mcpStateKeyring struct {
	active   []byte
	previous []byte
}

func deriveMCPStateKey(material string) []byte {
	sum := sha256.Sum256([]byte("agently-mcp-oauth-state\x00" + material))
	return sum[:]
}

// newMCPStateKeyring builds the keyring: auth.stateEncryptionKey (falling back
// to the delegated token encryption salt) is the active key and
// auth.stateEncryptionKeyPrevious covers the rotation grace period. Missing
// key material returns nil so callers fail loudly instead of sealing with a
// guessable key.
func newMCPStateKeyring(cfg *Config) *mcpStateKeyring {
	if cfg == nil {
		return nil
	}
	activeMaterial := strings.TrimSpace(cfg.StateEncryptionKey)
	if activeMaterial == "" {
		activeMaterial = cfg.DelegatedTokenEncryptionSalt()
	}
	if activeMaterial == "" {
		return nil
	}
	ring := &mcpStateKeyring{active: deriveMCPStateKey(activeMaterial)}
	if previous := strings.TrimSpace(cfg.StateEncryptionKeyPrevious); previous != "" && previous != activeMaterial {
		ring.previous = deriveMCPStateKey(previous)
	}
	return ring
}

func (k *mcpStateKeyring) seal(payload []byte) (string, error) {
	if k == nil || len(k.active) == 0 {
		return "", fmt.Errorf("mcp oauth state keyring is not configured")
	}
	block, err := aes.NewCipher(k.active)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, payload, []byte(mcpStateAAD))
	return mcpStateVersion + "." + base64RawURL(sealed), nil
}

func (k *mcpStateKeyring) open(state string) ([]byte, error) {
	if k == nil || len(k.active) == 0 {
		return nil, fmt.Errorf("mcp oauth state keyring is not configured")
	}
	state = strings.TrimSpace(state)
	prefix := mcpStateVersion + "."
	if !strings.HasPrefix(state, prefix) {
		return nil, fmt.Errorf("unsupported mcp oauth state version")
	}
	raw, err := base64RawURLDecode(strings.TrimPrefix(state, prefix))
	if err != nil {
		return nil, fmt.Errorf("malformed mcp oauth state")
	}
	keys := [][]byte{k.active}
	if len(k.previous) > 0 {
		keys = append(keys, k.previous)
	}
	var lastErr error
	for _, key := range keys {
		block, err := aes.NewCipher(key)
		if err != nil {
			lastErr = err
			continue
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			lastErr = err
			continue
		}
		if len(raw) <= gcm.NonceSize() {
			lastErr = fmt.Errorf("malformed mcp oauth state")
			continue
		}
		payload, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(mcpStateAAD))
		if err == nil {
			return payload, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("mcp oauth state authentication failed")
	}
	return nil, lastErr
}

// encryptMCPAuthState seals the state payload with the active AEAD key.
func (k *mcpStateKeyring) encryptMCPAuthState(state *MCPAuthState) (string, error) {
	if state == nil {
		return "", fmt.Errorf("mcp oauth state payload is required")
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return k.seal(payload)
}

// decryptMCPAuthState authenticates and decodes a state blob, trying the
// active key first and the previous key within the rotation grace window.
func (k *mcpStateKeyring) decryptMCPAuthState(state string) (*MCPAuthState, error) {
	payload, err := k.open(state)
	if err != nil {
		return nil, err
	}
	decoded := &MCPAuthState{}
	if err := json.Unmarshal(payload, decoded); err != nil {
		return nil, fmt.Errorf("malformed mcp oauth state payload")
	}
	return decoded, nil
}

// mcpStateHash is the non-secret single-use anchor stored in oauth_link_state:
// the hex SHA-256 of the opaque state blob handed to the provider.
func mcpStateHash(state string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(state)))
	return hex.EncodeToString(sum[:])
}

// mcpFlowHash deduplicates initiation across pods for one canonical user,
// provider, resource and normalized scope set. Scopes are deduplicated and
// sorted so ordering never creates a second flow.
func mcpFlowHash(canonicalUserID, providerRef, resource string, scopes []string) string {
	h := sha256.New()
	for _, part := range []string{strings.TrimSpace(canonicalUserID), strings.TrimSpace(providerRef), strings.TrimSpace(resource)} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	normalized := normalizeScopes(scopes)
	sorted := append([]string(nil), normalized...)
	sort.Strings(sorted)
	for _, scope := range sorted {
		h.Write([]byte(scope))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// mcpSessionHash binds state rows to one workspace session without storing the
// session identifier itself; the state key doubles as the HMAC key so hashes
// cannot be recomputed from a leaked table alone.
func (k *mcpStateKeyring) mcpSessionHash(sessionID string) string {
	if k == nil || len(k.active) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, k.active)
	mac.Write([]byte("session\x00" + strings.TrimSpace(sessionID)))
	return hex.EncodeToString(mac.Sum(nil))
}

// mcpSessionHashes returns the acceptable session hashes for a session ID:
// the active-key HMAC plus the previous-key HMAC during rotation grace, so
// flows initiated before a key rotation still bind to their session.
func (k *mcpStateKeyring) mcpSessionHashes(sessionID string) []string {
	if k == nil || len(k.active) == 0 {
		return nil
	}
	hashes := []string{k.mcpSessionHash(sessionID)}
	if len(k.previous) > 0 {
		mac := hmac.New(sha256.New, k.previous)
		mac.Write([]byte("session\x00" + strings.TrimSpace(sessionID)))
		hashes = append(hashes, hex.EncodeToString(mac.Sum(nil)))
	}
	return hashes
}

// mcpCSRFToken derives the per-session CSRF token required on POST initiate
// and DELETE disconnect. It is keyed separately from the session hash.
func (k *mcpStateKeyring) mcpCSRFToken(sessionID string) string {
	if k == nil || len(k.active) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, k.active)
	mac.Write([]byte("csrf\x00" + strings.TrimSpace(sessionID)))
	return hex.EncodeToString(mac.Sum(nil))
}

// mcpCSRFTokenValid compares a presented CSRF token in constant time,
// accepting the previous key during rotation grace.
func (k *mcpStateKeyring) mcpCSRFTokenValid(sessionID, presented string) bool {
	presented = strings.TrimSpace(presented)
	if k == nil || len(k.active) == 0 || presented == "" {
		return false
	}
	if hmac.Equal([]byte(presented), []byte(k.mcpCSRFToken(sessionID))) {
		return true
	}
	if len(k.previous) > 0 {
		mac := hmac.New(sha256.New, k.previous)
		mac.Write([]byte("csrf\x00" + strings.TrimSpace(sessionID)))
		if hmac.Equal([]byte(presented), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			return true
		}
	}
	return false
}

// mcpStateTTL returns the state lifetime: the configured minutes clamped to
// the mandated 5–10 minute window, defaulting to 7 minutes.
func mcpStateTTL(cfg *Config) time.Duration {
	if cfg == nil || cfg.MCPLinkStateTTLMinutes <= 0 {
		return mcpStateDefaultTTL
	}
	ttl := time.Duration(cfg.MCPLinkStateTTLMinutes) * time.Minute
	if ttl < mcpStateMinTTL {
		return mcpStateMinTTL
	}
	if ttl > mcpStateMaxTTL {
		return mcpStateMaxTTL
	}
	return ttl
}

func mcpStateTTLForClient(cfg *Config, client *authcfg.OAuthClient) time.Duration {
	if authTimeout := oauthClientAuthTimeout(client); authTimeout != "" {
		if timeout, err := time.ParseDuration(authTimeout); err == nil {
			if timeout < mcpStateMinTTL {
				return mcpStateMinTTL
			}
			if timeout > mcpStateMaxTTL {
				return mcpStateMaxTTL
			}
			return timeout
		}
	}
	return mcpStateTTL(cfg)
}

// oauthClientAuthTimeout keeps agently-core source-compatible with the prior
// viant/mcp release while the authTimeout field rolls through dependent
// modules. New MCP versions expose the field; older versions safely retain
// the five-minute default.
func oauthClientAuthTimeout(client *authcfg.OAuthClient) string {
	if client == nil {
		return ""
	}
	value := reflect.ValueOf(client)
	if value.Kind() == reflect.Ptr {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return ""
	}
	field := value.FieldByName("AuthTimeout")
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return strings.TrimSpace(field.String())
}
