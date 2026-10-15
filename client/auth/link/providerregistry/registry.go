// Package providerregistry implements the workspace OAuth provider registry.
// It loads provider definitions from <workspace>/oauth/providers/*.yaml and
// implements the generic viant/mcp ProviderRegistry interface plus the
// Agently-specific client selection, kill-switch and fingerprint surface.
package providerregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	"github.com/viant/mcp/client/auth/link/provider"
)

// GlobalKillSwitchEnv disables all delegated MCP OAuth when set to a truthy
// value. A disabled switch blocks reuse, refresh, initiation and callback
// persistence; it never silently falls back to workspace credentials.
const GlobalKillSwitchEnv = "AGENTLY_MCP_DELEGATED_AUTH_DISABLED"

// GloballyDisabled reports the global delegated-auth kill switch state.
func GloballyDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(GlobalKillSwitchEnv))) {
	case "", "0", "false", "off", "no":
		return false
	}
	return true
}

// NotFoundError reports an unknown provider reference.
type NotFoundError struct{ Ref string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("oauth provider %q is not registered", e.Ref)
}

// DisabledError reports a provider disabled through a kill switch.
type DisabledError struct{ Ref string }

func (e *DisabledError) Error() string {
	return fmt.Sprintf("provider_disabled: oauth provider %q is disabled", e.Ref)
}

// IsDisabled reports whether err marks a disabled provider or kill switch.
func IsDisabled(err error) bool {
	var disabled *DisabledError
	return errors.As(err, &disabled)
}

// IsNotFound reports whether err marks an unknown provider reference.
func IsNotFound(err error) bool {
	var notFound *NotFoundError
	return errors.As(err, &notFound)
}

// Loader abstracts the workspace provider repository for tests.
type Loader interface {
	List(ctx context.Context) ([]string, error)
	Load(ctx context.Context, name string) (*oauthprovider.Document, error)
}

// dirChecker is optionally implemented by loaders that can distinguish a
// genuinely missing oauth/providers directory from an IO failure. Only a
// missing directory is treated as "no providers configured"; every other
// List error fails closed.
type dirChecker interface {
	DirExists(ctx context.Context) (bool, error)
}

// inlineOverlay is the in-memory runtime registry of validated inline MCP
// providers, keyed by canonical owner and stable provider ID. It is shared
// process-wide so registry instances observe the same owner's inline provider.
// Trusted no-user startup definitions remain a separate authoritative scope. It does not
// survive restarts: until the defining MCP configuration is loaded again,
// resolution fails closed with a not-found error.
type inlineProviderKey struct{ owner, id string }

type inlineOverlay struct {
	mu    sync.RWMutex
	byRef map[inlineProviderKey]*authcfg.OAuthProvider
}

func newInlineOverlay() *inlineOverlay {
	return &inlineOverlay{byRef: map[inlineProviderKey]*authcfg.OAuthProvider{}}
}

// sharedInlineOverlay backs registries created with New so inline providers
// registered through the MCP manager are visible to the refresh watcher's
// registry instance within the same process.
var sharedInlineOverlay = newInlineOverlay()

func (o *inlineOverlay) register(owner, id string, provider *authcfg.OAuthProvider) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if owner != "" {
		if global := o.byRef[inlineProviderKey{id: id}]; global != nil {
			if authcfg.NormalizeIssuer(global.Issuer) != authcfg.NormalizeIssuer(provider.Issuer) {
				return fmt.Errorf("inline oauth provider conflicts with trusted startup provider")
			}
			return nil
		}
	}
	key := inlineProviderKey{owner: owner, id: id}
	if existing := o.byRef[key]; existing != nil {
		if !equalProviderDefinition(existing, provider) {
			return fmt.Errorf("inline oauth provider %q conflicts with a different inline definition already registered under the same id", id)
		}
		return nil
	}
	o.byRef[key] = provider.Clone()
	return nil
}

func (o *inlineOverlay) provider(owner, id string) *authcfg.OAuthProvider {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if provider := o.byRef[inlineProviderKey{id: id}]; provider != nil {
		return provider.Clone()
	}
	if provider := o.byRef[inlineProviderKey{owner: owner, id: id}]; provider != nil {
		return provider.Clone()
	}
	return nil
}

func (o *inlineOverlay) snapshot(owner string) map[string]*authcfg.OAuthProvider {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := map[string]*authcfg.OAuthProvider{}
	for key, provider := range o.byRef {
		if key.owner == owner {
			result[key.id] = provider.Clone()
		}
	}
	// Trusted startup definitions are authoritative over same-ID user definitions.
	for key, provider := range o.byRef {
		if key.owner == "" {
			result[key.id] = provider.Clone()
		}
	}
	return result
}
func (o *inlineOverlay) allProviders() []*authcfg.OAuthProvider {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]*authcfg.OAuthProvider, 0, len(o.byRef))
	for _, provider := range o.byRef {
		result = append(result, provider.Clone())
	}
	return result
}

// equalProviderDefinition compares two provider definitions by canonical JSON;
// definitions carry configuration references only, never secret material.
func equalProviderDefinition(left, right *authcfg.OAuthProvider) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

// cacheTTL bounds staleness between workspace edits and registry state so hot
// reload needs no watcher plumbing.
const cacheTTL = 10 * time.Second

type snapshot struct {
	byRef       map[string]*oauthprovider.Document
	fingerprint string
	loadedAt    time.Time
}

// Registry loads, validates and serves workspace OAuth provider definitions.
// It implements github.com/viant/mcp/client/auth/config.ProviderRegistry.
type Registry struct {
	loader  Loader
	ttl     time.Duration
	mu      sync.Mutex
	snap    *snapshot
	now     func() time.Time
	overlay *inlineOverlay
}

// New creates a registry over the default filesystem repository. It shares
// the process-wide inline overlay so inline MCP providers registered by one
// registry instance are resolvable by every other.
func NewWithLoader(loader Loader) *Registry {
	return &Registry{loader: loader, ttl: cacheTTL, now: time.Now, overlay: newInlineOverlay()}
}

// Invalidate drops the cached snapshot so the next access reloads from the
// workspace (hot reload / admin change).
func (r *Registry) Invalidate() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.snap = nil
	r.mu.Unlock()
}

func (r *Registry) load(ctx context.Context) (*snapshot, error) {
	if r == nil || r.loader == nil {
		return nil, fmt.Errorf("provider registry is not configured")
	}
	now := r.now()
	r.mu.Lock()
	if r.snap != nil && now.Sub(r.snap.loadedAt) < r.ttl {
		snap := r.snap
		r.mu.Unlock()
		return snap, nil
	}
	r.mu.Unlock()

	names, err := r.loader.List(ctx)
	if err != nil {
		// Only a genuinely missing oauth/providers directory means "no
		// providers configured". IO, decode and configuration errors fail
		// closed: serving an empty registry on a transient failure would
		// silently disable delegated auth or mask misconfiguration.
		if checker, ok := r.loader.(dirChecker); ok {
			if exists, checkErr := checker.DirExists(ctx); checkErr == nil && !exists {
				return &snapshot{byRef: map[string]*oauthprovider.Document{}, loadedAt: now}, nil
			}
		}
		return nil, fmt.Errorf("oauth provider registry: list providers: %w", err)
	}
	byRef := map[string]*oauthprovider.Document{}
	for _, name := range names {
		doc, err := r.loader.Load(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("oauth provider %q: %w", name, err)
		}
		if doc == nil {
			continue
		}
		expandDocumentEnv(doc)
		if strings.TrimSpace(doc.ID) == "" {
			doc.ID = strings.TrimSpace(name)
		}
		doc.Issuer = strings.TrimSpace(doc.Issuer)
		if err := doc.OAuthProvider.Validate(); err != nil {
			return nil, err
		}
		ref := strings.TrimSpace(doc.ID)
		if _, dup := byRef[ref]; dup {
			return nil, fmt.Errorf("oauth provider reference %q is duplicated in workspace", ref)
		}
		byRef[ref] = doc
	}
	snap := &snapshot{byRef: byRef, fingerprint: fingerprint(byRef), loadedAt: now}
	r.mu.Lock()
	r.snap = snap
	r.mu.Unlock()
	return snap, nil
}

// References lists configured provider IDs without returning secret references.
func (r *Registry) References(ctx context.Context) ([]string, error) {
	snap, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(snap.byRef))
	for ref := range snap.byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs, nil
}

// Provider returns the provider document registered under ref, including its
// administrative state. Disabled providers are returned so callers can apply
// the kill switch explicitly; ResolveProvider rejects them.
func (r *Registry) Provider(ctx context.Context, ref string) (*oauthprovider.Document, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, &NotFoundError{Ref: ref}
	}
	snap, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	doc, ok := snap.byRef[ref]
	if !ok || doc == nil {
		return nil, &NotFoundError{Ref: ref}
	}
	return doc, nil
}

// ResolveProvider implements viant/mcp config.ProviderRegistry. Disabled
// providers and the global kill switch fail closed with a typed error.
// Registry-file providers win; the runtime inline overlay answers only for
// references absent from the workspace registry.
func (r *Registry) ResolveProvider(ctx context.Context, ref string) (*authcfg.OAuthProvider, error) {
	if lookupOwnerPresent(ctx) {
		if _, err := inlineLookupOwner(ctx); err != nil {
			return nil, err
		}
	}
	if GloballyDisabled() {
		return nil, &DisabledError{Ref: strings.TrimSpace(ref)}
	}
	doc, err := r.Provider(ctx, ref)
	if err != nil {
		if IsNotFound(err) && r.overlay != nil {
			owner, scopeErr := inlineLookupOwner(ctx)
			if scopeErr != nil {
				return nil, scopeErr
			}
			if inline := r.overlay.provider(owner, strings.TrimSpace(ref)); inline != nil {
				return inline, nil
			}
		}
		return nil, err
	}
	if doc.Disabled {
		return nil, &DisabledError{Ref: strings.TrimSpace(ref)}
	}
	return doc.OAuthProvider.Clone(), nil
}

// RegisterInline records a validated inline MCP provider in the runtime
// overlay keyed by its stable ID. Registry-file providers always win: an
// inline definition whose ID matches a workspace file provider is accepted
// only when both agree on the normalized issuer, and resolution keeps serving
// the file definition. Conflicting definitions fail so a delegated MCP can
// never silently switch providers.
func (r *Registry) RegisterInline(ctx context.Context, provider *authcfg.OAuthProvider) error {
	if lookupOwnerPresent(ctx) {
		return fmt.Errorf("inline provider lookup context cannot authorize registration")
	}
	if r == nil || r.overlay == nil {
		return fmt.Errorf("provider registry is not configured for inline providers")
	}
	if provider == nil {
		return fmt.Errorf("inline oauth provider was nil")
	}
	id := strings.TrimSpace(provider.ID)
	if id == "" {
		return fmt.Errorf("inline oauth provider requires a stable id for registration")
	}
	if err := provider.Validate(); err != nil {
		return err
	}
	owner, scopeErr := inlineOwner(ctx)
	if scopeErr != nil {
		return scopeErr
	}
	snap, err := r.load(ctx)
	if err != nil {
		return err
	}
	if doc, ok := snap.byRef[id]; ok && doc != nil {
		if authcfg.NormalizeIssuer(doc.Issuer) != authcfg.NormalizeIssuer(provider.Issuer) {
			return fmt.Errorf("inline oauth provider %q conflicts with the workspace registry provider of the same id (issuer %q vs %q); registry-file providers win — align or rename the inline definition", id, provider.Issuer, doc.Issuer)
		}
		// File definition wins; nothing to overlay.
		return nil
	}
	return r.overlay.register(owner, id, provider)
}

// MatchIssuer implements viant/mcp config.ProviderRegistry. It hard-fails
// when more than one enabled provider has the normalized issuer — ordering is
// never a tie-break for challenge learning.
func (r *Registry) MatchIssuer(ctx context.Context, issuer string) (*authcfg.OAuthProvider, error) {
	if lookupOwnerPresent(ctx) {
		if _, err := inlineLookupOwner(ctx); err != nil {
			return nil, err
		}
	}
	if GloballyDisabled() {
		return nil, &DisabledError{Ref: authcfg.NormalizeIssuer(issuer)}
	}
	normalized := authcfg.NormalizeIssuer(issuer)
	if normalized == "" {
		return nil, fmt.Errorf("oauth issuer is empty")
	}
	snap, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	var matched *oauthprovider.Document
	for _, doc := range snap.byRef {
		if doc == nil || doc.Disabled {
			continue
		}
		if authcfg.NormalizeIssuer(doc.Issuer) != normalized {
			continue
		}
		if matched != nil {
			return nil, fmt.Errorf("oauth issuer %q is ambiguous: providers %q and %q share it", normalized, matched.ID, doc.ID)
		}
		matched = doc
	}
	if matched != nil {
		return matched.OAuthProvider.Clone(), nil
	}
	// Registry-file providers win; inline overlay providers answer only when
	// no file provider carries the issuer, with the same ambiguity rule.
	if r.overlay != nil {
		var inline *authcfg.OAuthProvider
		owner, scopeErr := inlineLookupOwner(ctx)
		if scopeErr != nil {
			return nil, scopeErr
		}
		for _, provider := range r.overlay.snapshot(owner) {
			if provider == nil || authcfg.NormalizeIssuer(provider.Issuer) != normalized {
				continue
			}
			if inline != nil {
				return nil, fmt.Errorf("oauth issuer %q is ambiguous: inline providers %q and %q share it", normalized, inline.ID, provider.ID)
			}
			inline = provider
		}
		if inline != nil {
			return inline.Clone(), nil
		}
	}
	return nil, &NotFoundError{Ref: normalized}
}

// Client resolves a client registration within a provider, applying the
// provider defaultClient when clientRef is empty. Selection must be
// unambiguous.
func (r *Registry) Client(ctx context.Context, providerRef, clientRef string) (*authcfg.OAuthClient, string, error) {
	provider, err := r.ResolveProvider(ctx, providerRef)
	if err != nil {
		return nil, "", err
	}
	return provider.Client(strings.TrimSpace(clientRef))
}

// MaxRefreshLead returns the largest configured refresh lead across all
// enabled providers and clients; the background watcher uses it as its broad
// scan horizon before applying each provider's own policy.
func (r *Registry) MaxRefreshLead(ctx context.Context) time.Duration {
	snap, err := r.load(ctx)
	if err != nil {
		return 0
	}
	var max time.Duration
	for _, doc := range snap.byRef {
		if doc == nil || doc.Disabled {
			continue
		}
		for _, client := range doc.Clients {
			if client == nil {
				continue
			}
			if lead, err := client.RefreshLeadDuration(); err == nil && lead > max {
				max = lead
			}
		}
	}
	// Only a numeric scan horizon spans owners; lookup/matching never do.
	if r.overlay != nil {
		for _, provider := range r.overlay.allProviders() {
			if provider == nil {
				continue
			}
			for _, client := range provider.Clients {
				if client == nil {
					continue
				}
				if lead, err := client.RefreshLeadDuration(); err == nil && lead > max {
					max = lead
				}
			}
		}
	}
	return max
}

// Fingerprint returns the non-secret configuration fingerprint used for audit
// records and metadata-cache invalidation.
func (r *Registry) Fingerprint(ctx context.Context) (string, error) {
	snap, err := r.load(ctx)
	if err != nil {
		return "", err
	}
	return snap.fingerprint, nil
}

// fingerprint hashes the canonical JSON of all provider documents. ConfigURL
// values are secret references, not secrets, so they are safe to include.
func fingerprint(byRef map[string]*oauthprovider.Document) string {
	refs := make([]string, 0, len(byRef))
	for ref := range byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	hash := sha256.New()
	for _, ref := range refs {
		data, err := json.Marshal(byRef[ref])
		if err != nil {
			continue
		}
		hash.Write([]byte(ref))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

var envTemplate = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?}`)

func expandEnvString(value string) string {
	if !strings.Contains(value, "${") {
		return value
	}
	return envTemplate.ReplaceAllStringFunc(value, func(match string) string {
		parts := envTemplate.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		if current, ok := os.LookupEnv(parts[1]); ok && current != "" {
			return current
		}
		if len(parts) >= 4 {
			return parts[3]
		}
		return ""
	})
}

func expandDocumentEnv(doc *oauthprovider.Document) {
	if doc == nil {
		return
	}
	doc.Issuer = expandEnvString(doc.Issuer)
	doc.DiscoveryURL = expandEnvString(doc.DiscoveryURL)
	for _, client := range doc.Clients {
		if client == nil {
			continue
		}
		client.ConfigURL = expandEnvString(client.ConfigURL)
		client.RedirectURI = expandEnvString(client.RedirectURI)
	}
}
