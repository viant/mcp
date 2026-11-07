// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"
)

// MCPAuthBinding is a validated, non-secret authentication binding learned
// from an MCP 401 protected-resource challenge. It records where the metadata
// came from and which approved provider/client it resolved to — never tokens,
// codes, verifiers or client secrets. Explicit MCP configuration always
// overrides a learned binding.
type MCPAuthBinding struct {
	ServerName      string    `json:"serverName"`
	Origin          string    `json:"origin"`
	MetadataURL     string    `json:"metadataURL"`
	ProviderRef     string    `json:"providerRef"`
	ClientRef       string    `json:"clientRef,omitempty"`
	Issuer          string    `json:"issuer"`
	Resource        string    `json:"resource"`
	ScopesSupported []string  `json:"scopesSupported,omitempty"`
	ETag            string    `json:"etag,omitempty"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

// Valid reports whether the binding is usable at now.
func (b *MCPAuthBinding) Valid(now time.Time) bool {
	if b == nil || strings.TrimSpace(b.ProviderRef) == "" || strings.TrimSpace(b.Issuer) == "" {
		return false
	}
	return b.ExpiresAt.After(now)
}

// learnedBindingTTL bounds how long a learned binding is trusted without
// re-validation against fresh challenge metadata.
const learnedBindingTTL = time.Hour

// AuthBindingStore persists learned bindings in memory plus the per-machine
// workspace StateStore (version-one contract: learned state is node-local;
// production MCP definitions should declare providerRef explicitly).
type StateStore interface {
	StatePath(context.Context, string) (string, error)
}

type AuthBindingStore struct {
	state StateStore
	mu    sync.Mutex
	cache map[string]*MCPAuthBinding
	now   func() time.Time
}

// NewAuthBindingStore creates a binding store over a workspace StateStore;
// nil keeps learned bindings in memory for this manager instance.
func NewAuthBindingStore(state StateStore) *AuthBindingStore {
	return &AuthBindingStore{state: state, cache: map[string]*MCPAuthBinding{}, now: time.Now}
}

func (s *AuthBindingStore) filePath(ctx context.Context, serverName string) (string, error) {
	dir, err := s.state.StatePath(ctx, "mcpauth/bindings")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sanitizeBindingName(serverName)+".json"), nil
}

func sanitizeBindingName(name string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	if builder.Len() == 0 {
		return "unnamed"
	}
	return builder.String()
}

// Save persists a validated binding (memory + state file, owner-only mode).
func (s *AuthBindingStore) Save(ctx context.Context, binding *MCPAuthBinding) error {
	if s == nil || binding == nil || strings.TrimSpace(binding.ServerName) == "" {
		return fmt.Errorf("auth binding requires a server name")
	}
	s.mu.Lock()
	s.cache[binding.ServerName] = binding
	s.mu.Unlock()
	if s.state == nil {
		return nil
	}
	path, err := s.filePath(ctx, binding.ServerName)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// Load returns the unexpired binding for a server; nil when absent/expired.
func (s *AuthBindingStore) Load(ctx context.Context, serverName string) (*MCPAuthBinding, error) {
	if s == nil {
		return nil, nil
	}
	now := s.now()
	s.mu.Lock()
	if cached, ok := s.cache[serverName]; ok {
		s.mu.Unlock()
		if cached.Valid(now) {
			return cached, nil
		}
		return nil, nil
	}
	s.mu.Unlock()
	if s.state == nil {
		return nil, nil
	}
	path, err := s.filePath(ctx, serverName)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	binding := &MCPAuthBinding{}
	if err := json.Unmarshal(data, binding); err != nil {
		return nil, nil
	}
	s.mu.Lock()
	s.cache[serverName] = binding
	s.mu.Unlock()
	if !binding.Valid(now) {
		return nil, nil
	}
	return binding, nil
}

// Invalidate drops the binding for a server (changed issuer/resource
// metadata, credential change).
func (s *AuthBindingStore) Invalidate(ctx context.Context, serverName string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.cache, serverName)
	s.mu.Unlock()
	if s.state == nil {
		return
	}
	if path, err := s.filePath(ctx, serverName); err == nil {
		_ = os.Remove(path)
	}
}

// WithAuthBindingStore installs the learned-binding store used for delegated
// challenge-mode servers.
func WithAuthBindingStore(store *AuthBindingStore) Option {
	return func(m *Manager) error { m.bindings = store; return nil }
}

// originOf returns scheme://host of an absolute URL; empty otherwise.
func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// learnAuthBinding validates and persists a challenge-derived binding.
// Validation is strict and fails closed: HTTPS metadata (loopback exempt for
// tests), resource origin equal to the MCP transport origin, an unambiguous
// approved provider for the issuer, and an unambiguous default client. Unknown
// issuers never receive known client credentials because MatchIssuer only
// answers from the approved registry.
func (m *Manager) learnAuthBinding(ctx context.Context, serverName, transportURL string, issuer, resource, metadataURL string, scopes []string) {
	if m == nil || m.bindings == nil || m.providerRegistry == nil {
		return
	}
	issuer = strings.TrimSpace(issuer)
	resource = strings.TrimSpace(resource)
	metadataURL = strings.TrimSpace(metadataURL)
	if issuer == "" || metadataURL == "" {
		return
	}
	metaParsed, err := url.Parse(metadataURL)
	if err != nil || metaParsed.Host == "" {
		return
	}
	if metaParsed.Scheme != "https" && !isLoopbackBindingHost(metaParsed.Hostname()) {
		log.Printf("[warn][mcp-auth-binding] server=%q rejected: metadata url is not https", serverName)
		return
	}
	transportOrigin := originOf(transportURL)
	if resource != "" && transportOrigin != "" && originOf(resource) != transportOrigin {
		log.Printf("[warn][mcp-auth-binding] server=%q rejected: resource origin does not match transport origin", serverName)
		return
	}
	provider, err := m.providerRegistry.MatchIssuer(ctx, issuer)
	if err != nil || provider == nil {
		log.Printf("[warn][mcp-auth-binding] server=%q rejected: issuer is not an approved provider", serverName)
		return
	}
	// Learning resolves the client through the provider defaultClient and
	// fails closed when no unambiguous default exists.
	_, clientName, err := provider.Client("")
	if err != nil {
		log.Printf("[warn][mcp-auth-binding] server=%q rejected: no unambiguous default client for provider %q", serverName, provider.ID)
		return
	}
	binding := &MCPAuthBinding{
		ServerName:      serverName,
		Origin:          transportOrigin,
		MetadataURL:     metadataURL,
		ProviderRef:     strings.TrimSpace(provider.ID),
		ClientRef:       clientName,
		Issuer:          authcfg.NormalizeIssuer(issuer),
		Resource:        resource,
		ScopesSupported: append([]string(nil), scopes...),
		ExpiresAt:       time.Now().Add(learnedBindingTTL),
	}
	if err := m.bindings.Save(ctx, binding); err != nil {
		log.Printf("[warn][mcp-auth-binding] server=%q persist failed: %v", serverName, err)
		return
	}
	log.Printf("[info][mcp-auth-binding] server=%q learned provider=%q issuer=%q", serverName, binding.ProviderRef, binding.Issuer)
}

// applyLearnedBinding fills the missing providerRef of a challenge-mode
// delegated config from a validated learned binding so the next client
// resolves eagerly. Explicit configuration always wins: servers with an
// explicit providerRef or inline provider are never touched. A binding whose
// origin or resource disagrees with the configuration is invalidated instead
// of applied.
func (m *Manager) applyLearnedBinding(ctx context.Context, serverName, transportURL string, auth *mcp.ClientAuth) {
	if m == nil || m.bindings == nil || auth == nil {
		return
	}
	if strings.TrimSpace(auth.ProviderRef) != "" || auth.InlineProvider != nil {
		return
	}
	binding, err := m.bindings.Load(ctx, serverName)
	if err != nil || binding == nil {
		return
	}
	if origin := originOf(transportURL); origin != "" && binding.Origin != "" && binding.Origin != origin {
		m.bindings.Invalidate(ctx, serverName)
		return
	}
	if configured := strings.TrimSpace(auth.Resource); configured != "" && binding.Resource != "" && configured != binding.Resource {
		m.bindings.Invalidate(ctx, serverName)
		return
	}
	auth.ProviderRef = binding.ProviderRef
	if strings.TrimSpace(auth.ClientRef) == "" {
		auth.ClientRef = binding.ClientRef
	}
	if strings.TrimSpace(auth.Resource) == "" {
		auth.Resource = binding.Resource
	}
	log.Printf("[info][mcp-auth-binding] server=%q applied learned provider=%q", serverName, binding.ProviderRef)
}

// crossCheckState throttles the once-per-server explicit-config metadata
// cross-check.
type crossCheckState struct {
	checkedAt time.Time
	err       error
}

var (
	crossCheckMu      sync.Mutex
	crossCheckByName  = map[string]*crossCheckState{}
	crossCheckRecheck = 15 * time.Minute
)

// crossCheckExplicitMetadata performs the one-time protected-resource
// metadata fetch for explicitly configured delegated servers and compares the
// advertised authorization server and resource with the explicit
// configuration. A fetch failure is tolerated (logged, skipped) so offline
// deployments keep working; a successful fetch that disagrees with explicit
// configuration is a configuration error and fails closed — it is never an
// opportunity to silently rewrite providerRef.
func (m *Manager) crossCheckExplicitMetadata(ctx context.Context, serverName, transportURL string, requirementIssuer, requirementResource, configuredMetadataURL string) error {
	transportOrigin := originOf(transportURL)
	metadataURL := strings.TrimSpace(configuredMetadataURL)
	if metadataURL != "" {
		parsed, err := url.Parse(metadataURL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || originOf(metadataURL) != transportOrigin {
			return fmt.Errorf("mcp server %q: protectedResourceMetadataURL must be HTTPS on the transport origin", serverName)
		}
	}
	if transportOrigin == "" || !strings.HasPrefix(transportOrigin, "https://") {
		// Loopback/dev transports have no trustworthy well-known endpoint.
		return nil
	}
	if metadataURL == "" {
		metadataURL = transportOrigin + "/.well-known/oauth-protected-resource"
	}
	cacheKey := strings.Join([]string{serverName, transportURL, metadataURL, requirementIssuer, requirementResource}, "\n")
	crossCheckMu.Lock()
	if state, ok := crossCheckByName[cacheKey]; ok && time.Since(state.checkedAt) < crossCheckRecheck {
		err := state.err
		crossCheckMu.Unlock()
		return err
	}
	crossCheckMu.Unlock()

	metadata, err := authcfg.DiscoverProtectedResource(ctx, metadataURL, &authcfg.DiscoveryOptions{
		Timeout:        3 * time.Second,
		ExpectedOrigin: transportOrigin,
	})
	var checkErr error
	if err != nil || metadata == nil {
		if strings.TrimSpace(configuredMetadataURL) != "" {
			return fmt.Errorf("mcp server %q: configured protected-resource metadata unavailable: %w", serverName, err)
		}
		// Best-effort: absence or unreachability of metadata is not an error
		// for explicitly configured servers.
		log.Printf("[info][mcp-auth-binding] server=%q metadata cross-check skipped: %v", serverName, err)
	} else {
		if resource := strings.TrimSpace(metadata.Resource); resource != "" && strings.TrimSpace(requirementResource) != "" &&
			strings.TrimRight(resource, "/") != strings.TrimRight(requirementResource, "/") {
			checkErr = fmt.Errorf("mcp server %q: protected-resource metadata advertises resource %q but configuration requires %q", serverName, resource, requirementResource)
		}
		if checkErr == nil && strings.TrimSpace(requirementIssuer) != "" && len(metadata.AuthorizationServers) > 0 {
			matched := false
			for _, server := range metadata.AuthorizationServers {
				if authcfg.NormalizeIssuer(server) == authcfg.NormalizeIssuer(requirementIssuer) {
					matched = true
					break
				}
			}
			if !matched {
				checkErr = fmt.Errorf("mcp server %q: protected-resource metadata does not list configured issuer %q", serverName, requirementIssuer)
			}
		}
	}
	crossCheckMu.Lock()
	crossCheckByName[cacheKey] = &crossCheckState{checkedAt: time.Now(), err: checkErr}
	crossCheckMu.Unlock()
	return checkErr
}

func isLoopbackBindingHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
