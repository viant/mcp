// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"errors"
	"fmt"
	"github.com/viant/mcp/client/observe"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/viant/mcp"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	auth "github.com/viant/mcp/client/auth"
	authcfg "github.com/viant/mcp/client/auth/config"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	mcpcfg "github.com/viant/mcp/client/config"
)

// Provider returns client options for a given MCP server name.
type Provider interface {
	Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error)
}

type providerLister interface {
	Names(ctx context.Context) ([]string, error)
}

// inlineProviderRegistrar is optionally implemented by host provider
// registries that support registering validated inline MCP providers in a
// runtime overlay (see service/auth/providerregistry.Registry.RegisterInline).
type inlineProviderRegistrar interface {
	RegisterInline(ctx context.Context, provider *authcfg.OAuthProvider) error
}

// Option configures Manager. It can return an error which will be bubbled up by New.
type Option func(*Manager) error

// WithTTL sets idle TTL before reaping a client.
func WithTTL(ttl time.Duration) Option { return func(m *Manager) error { m.ttl = ttl; return nil } }

// WithHandlerFactory sets a factory for per-connection client handlers (for elicitation, etc.).
func WithHandlerFactory(newHandler func() protoclient.Handler) Option {
	return func(m *Manager) error { m.newHandler = newHandler; return nil }
}

// WithClientFactory injects a client constructor override. It is primarily
// useful for tests and constrained runtimes that need to provide a custom MCP
// transport implementation without replacing the rest of the manager.
func WithClientFactory(factory func(context.Context, string, string) (mcpclient.Interface, error)) Option {
	return func(m *Manager) error { m.newClientFn = factory; return nil }
}

// WithCookieJar injects a host-controlled CookieJar that will be applied to
// newly created MCP clients via ClientOptions, overriding any per-provider jar.
func WithCookieJar(jar http.CookieJar) Option {
	return func(m *Manager) error { m.cookieJar = jar; return nil }
}

// JarProvider returns a per-request CookieJar (e.g., per-user) chosen from context.
// When provided, it takes precedence over the static cookieJar set via WithCookieJar.
type JarProvider func(ctx context.Context) (http.CookieJar, error)

// WithCookieJarProvider injects a provider that selects a CookieJar per request (e.g., per user).
func WithCookieJarProvider(p JarProvider) Option {
	return func(m *Manager) error { m.jarProvider = p; return nil }
}

// WithAuthRoundTripper enables auth integration by attaching the provided
// RoundTripper as an Authorizer interceptor to created MCP clients.
func WithAuthRoundTripper(rt *authtransport.RoundTripper) Option {
	return func(m *Manager) error { m.authRT = rt; return nil }
}

// AuthRTProvider returns a per-request auth RoundTripper (e.g., per-user) chosen from context.
// When provided, it takes precedence over the static authRT set via WithAuthRoundTripper.
type AuthRTProvider func(ctx context.Context) *authtransport.RoundTripper

// WithAuthRoundTripperProvider injects a provider that selects an auth RoundTripper per request.
func WithAuthRoundTripperProvider(p AuthRTProvider) Option {
	return func(m *Manager) error { m.authRTProvider = p; return nil }
}

// UserIDExtractor returns a user identifier from context for pool isolation.
// When set, the pool key becomes "userID:convID" instead of just "convID"
// to prevent shared conversations from leaking MCP auth across users.
type UserIDExtractor func(ctx context.Context) string

// WithUserIDExtractor sets the function used to derive a user-scoped pool key.
func WithUserIDExtractor(fn UserIDExtractor) Option {
	return func(m *Manager) error { m.userIDFn = fn; return nil }
}

// TokenContextPreparer lets a host refresh its transport tokens in context.
// It must preserve caller identity. The shared pool owns no application store.
type TokenContextPreparer func(context.Context, string, bool) context.Context

func WithTokenContextPreparer(prepare TokenContextPreparer) Option {
	return func(m *Manager) error { m.prepareTokens = prepare; return nil }
}

// CredentialErrorHandler adapts a transport credential failure to a host's
// presentation without importing application login or persistence packages.
type CredentialErrorHandler func(context.Context, *mcpcfg.MCPClient, authcfg.Requirement, error) error

func WithCredentialErrorHandler(handler CredentialErrorHandler) Option {
	return func(m *Manager) error { m.credentialError = handler; return nil }
}

// WithCredentialResolver installs the host credential resolver used for MCP
// definitions with auth.mode=oauth (delegated). For those servers viant/mcp
// becomes the sole transport-level OAuth coordinator and the resolver the
// sole credential-policy implementation; legacy servers are unaffected.
func WithCredentialResolver(resolver authcfg.CredentialResolver) Option {
	return func(m *Manager) error { m.credResolver = resolver; return nil }
}

// WithProviderRegistry installs the host OAuth provider registry so delegated
// requirement compilation can resolve providerRef before the first request.
func WithProviderRegistry(registry authcfg.ProviderRegistry) Option {
	return func(m *Manager) error { m.providerRegistry = registry; return nil }
}

// Manager caches MCP clients per (userID:conversationID, serverName) and handles idle reaping.
type Manager struct {
	localMu sync.RWMutex
	local   map[string]localRegistration

	clientEvictionListeners   map[uint64]func(ClientEviction)
	pendingClientEvictions    []ClientEviction
	changeListeners           map[uint64]func(string)
	changeListenerID          uint64
	resourceChangeListeners   map[uint64]func(string)
	resourceUpdatedListeners  map[uint64]func(string, string)
	resourceCatalogGeneration map[resourceCatalogScope]uint64
	resourceContentGeneration map[resourceCatalogScope]uint64
	persistentToolCatalog     bool
	toolCatalog               map[string]*toolCatalogPage
	toolCatalogGeneration     map[string]uint64
	protectedToolCatalog      map[string][32]byte
	visibleCatalogs           map[string]*visibleCatalog
	catalogSequence           uint64
	catalogEpoch              [16]byte
	prov                      Provider
	ttl                       time.Duration
	maxClients                int
	capacityUsed              int
	newHandler                func() protoclient.Handler
	newClientFn               func(context.Context, string, string) (mcpclient.Interface, error)
	cookieJar                 http.CookieJar
	jarProvider               JarProvider
	authRT                    *authtransport.RoundTripper
	authRTProvider            AuthRTProvider
	userIDFn                  UserIDExtractor
	prepareTokens             TokenContextPreparer
	credentialError           CredentialErrorHandler
	delegatedHTTPTransport    DelegatedHTTPTransportProvider
	credResolver              authcfg.CredentialResolver
	providerRegistry          authcfg.ProviderRegistry
	// bindings persists validated non-secret challenge-learned MCP auth
	// bindings (workspace StateStore); explicit configuration always wins.
	bindings *AuthBindingStore

	mu               sync.Mutex
	definitionBindMu sync.Mutex
	pool             map[string]map[string]*entry      // poolKey -> serverName -> entry
	inflight         map[string]map[string]*createCall // poolKey -> serverName -> in-flight client creation
	epoch            map[string]uint64                 // poolKey -> invalidation generation

	poolSummaryEvery time.Duration
	poolSummaryAt    time.Time
}

type entry struct {
	capacityHeld          bool
	resourceReceiver      [32]byte
	aliases               map[string]bool
	configFingerprint     [32]byte
	credentialFingerprint [32]byte
	client                mcpclient.Interface
	managed               *managedClient
	usedAt                time.Time
	active                int
	evicted               bool
	closed                bool
	epoch                 uint64
}

type createCall struct {
	configFingerprint     [32]byte
	credentialFingerprint [32]byte
	conflictLogged        bool
	ready                 chan struct{}
	entry                 *entry
	err                   error
	epoch                 uint64
}

func poolChangeFields(previous, current [32]byte) string {
	if previous == current {
		return "reason=credential_changed"
	}
	return fmt.Sprintf("reason=configuration_changed previous=%x current=%x", previous[:6], current[:6])
}

// New creates a Manager with the given Provider and options.
func New(prov Provider, opts ...Option) (*Manager, error) {
	m := &Manager{
		prov:     prov,
		ttl:      30 * time.Minute,
		pool:     map[string]map[string]*entry{},
		inflight: map[string]map[string]*createCall{},
		epoch:    map[string]uint64{},
		// Activity-driven summary: emitted from Get/Reconnect paths, not by
		// starting a separate goroutine.
		poolSummaryEvery: 15 * time.Minute,
		//poolSummaryEvery: 2 * time.Minute,
	}
	for _, o := range opts {
		if err := o(m); err != nil {
			return nil, fmt.Errorf("mcp manager option: %w", err)
		}
	}
	if m.bindings == nil {
		m.bindings = NewAuthBindingStore(nil)
	}
	return m, nil
}

// PreflightCredential resolves delegated credentials before a tool execution
// claim is acquired or any remote MCP request is made. Interactive hosts may
// block on a typed link-required result and invoke this method again after the
// same authorization interaction completes.
func (m *Manager) PreflightCredential(ctx context.Context, serverName string) error {
	if m != nil {
		if _, ok := m.localRegistration(serverName); ok {
			return m.checkLocalCollision(ctx, serverName)
		}
	}
	if m == nil || m.prov == nil {
		return nil
	}
	opts, err := m.prov.Options(ctx, strings.TrimSpace(serverName))
	if err == nil && opts != nil && opts.IsBrowserExecution() {
		return ErrBrowserExecutionRequired
	}
	if err != nil || opts == nil || opts.ClientOptions == nil || opts.ClientOptions.Auth == nil {
		return err
	}
	serverName = resolvedServiceName(opts, serverName)
	if opts.ClientOptions.Auth.IsDelegated() {
		m.applyLearnedBinding(ctx, serverName, opts.ClientOptions.Transport.URL, opts.ClientOptions.Auth)
	}
	if !opts.IsDelegatedAuth() {
		return nil
	}
	if opts.DisableDelegatedAuth {
		return fmt.Errorf("provider_disabled: delegated auth for mcp server %q is disabled by configuration", serverName)
	}
	if m.credResolver == nil {
		return fmt.Errorf("mcp server %q requires delegated oauth but no credential resolver is installed", serverName)
	}
	if err := opts.NormalizeDelegatedAuth(); err != nil {
		return fmt.Errorf("mcp server %q: %w", serverName, err)
	}
	auth := opts.ClientOptions.Auth
	auth.ExternalResolver = m.credResolver
	if auth.ProviderRegistry == nil {
		auth.ProviderRegistry = m.providerRegistry
	}
	if strings.TrimSpace(auth.ProviderRef) != "" && auth.ProviderRegistry == nil {
		return fmt.Errorf("mcp server %q: auth.providerRef %q requires an oauth provider registry, but none is installed", serverName, auth.ProviderRef)
	}
	if inline := auth.InlineProvider; inline != nil {
		if registrar, ok := auth.ProviderRegistry.(inlineProviderRegistrar); ok {
			if err := registrar.RegisterInline(ctx, inline); err != nil {
				return fmt.Errorf("mcp server %q: %w", serverName, err)
			}
		}
	}
	requirement, err := auth.CompileRequirement(ctx, serverName, opts.ClientOptions.Transport.URL)
	if err != nil {
		return err
	}
	_, err = m.credResolver.Resolve(ctx, *requirement)
	if m.credentialError != nil {
		return m.credentialError(ctx, opts, *requirement, err)
	}
	return err
}

// poolKey returns a user-scoped key for the connection pool.
// When a UserIDExtractor is configured, the key is "userID:convID" to
// prevent shared conversations from leaking MCP auth/tokens across users.
func (m *Manager) poolKey(ctx context.Context, convID string) string {
	if m.userIDFn != nil {
		if uid := strings.TrimSpace(m.userIDFn(ctx)); uid != "" {
			return uid + ":" + convID
		}
	}
	if uid := authctx.EffectiveUserID(ctx); uid != "" {
		return fmt.Sprintf("principal:%d:%s:%s", len(uid), uid, convID)
	}
	return convID
}

// Options exposes the underlying provider client options (authoring metadata,
// timeouts, etc.) for a given server name.
func (m *Manager) Options(ctx context.Context, serverName string) (result *mcpcfg.MCPClient, resultErr error) {
	span := observe.Start(ctx, "mcp.configuration", observe.Labels{Provider: serverName})
	defer func() { span.End(observe.OutcomeFor(resultErr), observe.CacheUnknown) }()
	if m == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	if registration, ok := m.localRegistration(serverName); ok {
		if err := m.checkLocalCollision(ctx, serverName); err != nil {
			return nil, err
		}
		return cloneLocalConfig(registration.options), nil
	}
	if m.prov == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	return m.prov.Options(ctx, serverName)
}

// Names lists configured MCP servers when the provider supports local
// inventory. It never initializes or contacts an MCP server.
func (m *Manager) Names(ctx context.Context) ([]string, error) {
	if m == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	if memo := namesMemoFromContext(ctx); memo != nil {
		scope, _, err := m.CatalogScopeForResources(ctx, "", nil, nil)
		if err != nil {
			return nil, err
		}
		generation := ""
		if provider, ok := m.prov.(interface{ DefinitionGeneration(context.Context) string }); ok {
			generation = provider.DefinitionGeneration(ctx)
		}
		if names, ok := memo.get(m, scope, generation); ok {
			return names, nil
		}
		names, err := m.names(ctx)
		if err == nil {
			memo.put(m, scope, generation, names)
		}
		return names, err
	}
	return m.names(ctx)
}

func (m *Manager) names(ctx context.Context) ([]string, error) {
	if m == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	var names []string
	if lister, ok := m.prov.(providerLister); ok {
		var err error
		names, err = lister.Names(ctx)
		if err != nil {
			return nil, err
		}
	}
	return m.mergeInventoryNames(ctx, names)
}

// CatalogNames lists discovery roots. Published tool connections remain in
// Names, but do not implicitly become new resource catalog roots.
func (m *Manager) CatalogNames(ctx context.Context) ([]string, error) {
	if m == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	return m.memoInventory(ctx, "catalog-roots", func() ([]string, error) { return m.catalogNames(ctx) })
}

func (m *Manager) catalogNames(ctx context.Context) ([]string, error) {
	if lister, ok := m.prov.(interface {
		CatalogNames(context.Context) ([]string, error)
	}); ok {
		names, err := lister.CatalogNames(ctx)
		if err != nil {
			return nil, err
		}
		return m.mergeInventoryNames(ctx, names)
	}
	return m.Names(ctx)
}

// CatalogProviderNames reuses routing claims within the existing request
// inventory snapshot. It does not perform authorization or create clients.
func (m *Manager) CatalogProviderNames(ctx context.Context, owner string) ([]string, error) {
	if m == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	return m.memoInventory(ctx, "catalog-owner:"+owner, func() ([]string, error) {
		names, err := m.CatalogNames(ctx)
		if err != nil {
			return nil, err
		}
		var selected []string
		for _, name := range names {
			options, err := m.Options(ctx, name)
			if err != nil {
				return nil, err
			}
			if options != nil && options.PrimitiveProviderIdentity == owner {
				selected = append(selected, name)
			}
		}
		return selected, nil
	})
}

func (m *Manager) memoInventory(ctx context.Context, kind string, read func() ([]string, error)) ([]string, error) {
	memo := namesMemoFromContext(ctx)
	if memo == nil {
		return read()
	}
	scope, _, err := m.CatalogScopeForResources(ctx, "", nil, nil)
	if err != nil {
		return nil, err
	}
	generation := ""
	if provider, ok := m.prov.(interface{ DefinitionGeneration(context.Context) string }); ok {
		generation = provider.DefinitionGeneration(ctx)
	}
	key := kind + "\x00" + generation
	if names, ok := memo.get(m, scope, key); ok {
		return names, nil
	}
	names, err := read()
	if err == nil {
		memo.put(m, scope, key, names)
	}
	return names, err
}

func (m *Manager) mergeInventoryNames(ctx context.Context, names []string) ([]string, error) {
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		options, err := browserOptions(ctx, m.prov, name)
		if err != nil {
			return nil, err
		}
		if options == nil || !options.IsBrowserExecution() {
			filtered = append(filtered, name)
		}
	}
	names = filtered
	m.localMu.RLock()
	defer m.localMu.RUnlock()
	for name := range m.local {
		if seen[name] {
			return nil, fmt.Errorf("mcp local connection collides with configured provider %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Get returns an MCP client for (user+convID, serverName), creating it if needed.
// When a UserIDExtractor is configured, the pool key includes the user ID to
// prevent shared conversations from leaking MCP auth/tokens across users.
func (m *Manager) Get(ctx context.Context, convID, serverName string) (result mcpclient.Interface, resultErr error) {
	defer m.dispatchClientEvictions()
	span := observe.Start(ctx, "mcp.pool.acquire", observe.Labels{Provider: serverName})
	defer func() { span.End(observe.OutcomeFor(resultErr), observe.CacheUnknown) }()
	requestedName := serverName
	if _, ok := m.localRegistration(serverName); ok {
		if err := m.checkLocalCollision(ctx, serverName); err != nil {
			return nil, err
		}
	}
	if m.prov == nil {
		if _, ok := m.localRegistration(serverName); !ok {
			return nil, errors.New("mcp manager: provider not configured")
		}
	}
	configured, configErr := m.Options(ctx, serverName)
	if configErr != nil || configured == nil {
		m.evictConfiguredClient(ctx, convID, serverName)
		if configErr != nil {
			return nil, configErr
		}
		return nil, errors.New("mcp manager: definition unavailable")
	}
	if configured != nil {
		if err := configured.ValidateExecutionLocation(); err != nil {
			return nil, err
		}
		if configured.IsBrowserExecution() {
			if _, err := configured.BrowserDescriptor(serverName); err != nil {
				return nil, err
			}
			return nil, ErrBrowserExecutionRequired
		}
	}
	serviceName := resolvedServiceName(configured, serverName)
	if serviceName != serverName {
		if _, local := m.localRegistration(serviceName); local {
			return nil, errors.New("mcp manager: resolved definition collides with local connection")
		}
	}
	serverName = serviceName
	fingerprint, err := effectiveConfigFingerprint(configured)
	if err != nil {
		return nil, err
	}
	// The server can advertise a different protocol surface to an anonymous
	// client. Reuse only a handshake made with the selected BFF credential.
	handshakeCtx := ctx
	var credentialFingerprint [32]byte
	if m.newClientFn == nil && !m.IsLocalClient(serverName) && usesUserTransportAuth(configured) {
		handshakeCtx = m.withAuthTokenContext(ctx, serverName, configured)
		credentialFingerprint = handshakeCredentialFingerprint(handshakeCtx)
	}
	key := m.poolKey(ctx, convID)
	receiver := m.resourceReceiver(ctx)
	m.mu.Lock()
	// Intentionally avoid `defer m.mu.Unlock()` here.
	// This lock must be released before client creation/network setup
	// (provider options, transport init) to prevent global manager stalls.
	// Maintain per-conversation client to correlate elicitation correctly.
	// A request can arrive before the periodic reaper. Do not renew an idle
	// expired session merely by looking it up; active operations retain it.
	if e := m.pool[key][serverName]; e != nil && e.client != nil && e.active == 0 && m.ttl > 0 && time.Since(e.usedAt) >= m.ttl {
		stale := m.evictEntryLocked(key, serverName, e, ClientIdleEvicted)
		m.mu.Unlock()
		closeClientBestEffort(stale)
		m.mu.Lock()
	}
	if e := m.pool[key][serverName]; e != nil && e.client != nil && (e.configFingerprint != fingerprint || e.credentialFingerprint != credentialFingerprint) {
		previousFingerprint := e.configFingerprint
		stale := m.evictEntryLocked(key, serverName, e)
		m.mu.Unlock()
		log.Printf("[info][mcp-client-pool] configuration_replaced server=%q scope_type=%s %s action=refresh", poolConfigLogName(serverName), poolScopeType(key, serverName), poolChangeFields(previousFingerprint, fingerprint))
		closeClientBestEffort(stale)
		m.mu.Lock()
	}
	if e := m.pool[key][serverName]; e != nil && e.client != nil && (e.configFingerprint != fingerprint || e.credentialFingerprint != credentialFingerprint) {
		previousFingerprint := e.configFingerprint
		m.mu.Unlock()
		log.Printf("[warn][mcp-client-pool] configuration_conflict server=%q scope_type=%s %s phase=concurrent_replacement action=reject", poolConfigLogName(serverName), poolScopeType(key, serverName), poolChangeFields(previousFingerprint, fingerprint))
		return nil, errors.New("mcp manager: configuration changed during client replacement")
	}
	if e := m.pool[key][serverName]; e != nil && e.client != nil {
		if e.aliases == nil {
			e.aliases = map[string]bool{}
		}
		e.aliases[requestedName] = true
		e.usedAt = time.Now()
		client := m.ensureManagedLocked(key, serverName, e)
		summary := m.poolSummaryLogLinesLocked(time.Now())
		m.mu.Unlock()
		logPoolSummary(summary)
		return client, nil
	}
	if call := m.inflight[key][serverName]; call != nil && (call.configFingerprint != fingerprint || call.credentialFingerprint != credentialFingerprint) {
		warn := !call.conflictLogged
		call.conflictLogged = true
		previousFingerprint := call.configFingerprint
		m.mu.Unlock()
		if warn {
			log.Printf("[warn][mcp-client-pool] configuration_conflict server=%q scope_type=%s %s phase=creation_inflight action=reject", poolConfigLogName(serverName), poolScopeType(key, serverName), poolChangeFields(previousFingerprint, fingerprint))
		}
		return nil, errors.New("mcp manager: configuration changed during client creation")
	}
	if call := m.inflight[key][serverName]; call != nil {
		m.mu.Unlock()
		waitTiming := observe.Start(ctx, "mcp.pool.wait", observe.Labels{Provider: serverName})
		select {
		case <-call.ready:
			waitTiming.End(observe.OutcomeFor(call.err), observe.CacheUnknown)
			if call.entry == nil || call.entry.managed == nil {
				if call.err != nil {
					return nil, call.err
				}
				return nil, errors.New("mcp manager: client creation returned nil")
			}
			return m.Get(ctx, convID, requestedName)
		case <-ctx.Done():
			waitTiming.End(observe.Canceled, observe.CacheUnknown)
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); m.maxClients > 0 && err != nil {
		m.mu.Unlock()
		return nil, err
	}
	retired, capacityErr := m.reserveClientSlotLocked()
	if capacityErr != nil {
		m.mu.Unlock()
		closeClientBestEffort(retired)
		return nil, capacityErr
	}
	reservedCapacity := m.maxClients > 0
	if m.inflight[key] == nil {
		m.inflight[key] = map[string]*createCall{}
	}
	call := &createCall{ready: make(chan struct{}), epoch: m.epoch[key], configFingerprint: fingerprint, credentialFingerprint: credentialFingerprint}
	m.inflight[key][serverName] = call
	m.mu.Unlock()
	closeClientBestEffort(retired)

	newClient := func(ctx context.Context, conversation, server string) (mcpclient.Interface, error) {
		// Resolve the existing delegated link before metadata discovery or transport
		// initialization. Missing links must not create a remote MCP connection.
		// Keep this inside ordinary creation singleflight, outside the pool lock;
		// pooled clients and explicit local/custom factories keep their lifecycle.
		if configured.IsDelegatedAuth() {
			if err := m.PreflightCredential(ctx, requestedName); err != nil {
				return nil, err
			}
		}
		return m.newClientWithOptions(ctx, conversation, server, configured)
	}
	if registration, ok := m.localRegistration(serverName); ok {
		newClient = func(ctx context.Context, _, _ string) (mcpclient.Interface, error) {
			client, err := registration.factory(ctx)
			if err == nil && client != nil {
				m.installLocalNotifications(ctx, serverName, client)
			}
			return client, err
		}
	} else if m.newClientFn != nil {
		newClient = func(ctx context.Context, conversation, server string) (mcpclient.Interface, error) {
			client, err := m.newClientFn(ctx, conversation, server)
			if err == nil && client != nil {
				m.installLocalNotifications(ctx, server, client)
			}
			return client, err
		}
	}
	createTiming := observe.Start(ctx, "mcp.pool.create", observe.Labels{Provider: serverName})
	client, err := newClient(handshakeCtx, convID, serverName)
	createTiming.End(observe.OutcomeFor(err), observe.CacheMiss)
	if reservedCapacity && err != nil && client != nil {
		closeClientBestEffort(client)
		client = nil
	}
	if reservedCapacity && ctx.Err() != nil {
		closeClientBestEffort(client)
		client, err = nil, ctx.Err()
	}
	if err == nil {
		current, configErr := m.Options(ctx, requestedName)
		currentFingerprint, fingerprintErr := effectiveConfigFingerprint(current)
		if configErr != nil || fingerprintErr != nil || currentFingerprint != fingerprint {
			closeClientBestEffort(client)
			client = nil
			if configErr != nil {
				err = configErr
			} else {
				err = errors.New("mcp manager: configuration changed during client creation")
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	capacityTransferred := false
	defer func() {
		if reservedCapacity && !capacityTransferred {
			m.capacityUsed--
		}
	}()
	delete(m.inflight[key], serverName)
	if len(m.inflight[key]) == 0 {
		delete(m.inflight, key)
	}
	call.err = err
	if err != nil {
		close(call.ready)
		return nil, err
	}
	if client == nil {
		call.err = errors.New("mcp manager: nil client returned")
		close(call.ready)
		return nil, call.err
	}
	if subscribed, ok := client.(*sensitiveClient); ok && subscribed.subscriptionLost.Load() {
		if reservedCapacity {
			closeClientBestEffort(client)
		}
		call.err = errors.New("mcp manager: resource subscription ended during client creation")
		close(call.ready)
		return nil, call.err
	}
	if m.epoch[key] != call.epoch {
		call.err = errors.New("mcp manager: client creation discarded after conversation close")
		close(call.ready)
		closeClientBestEffort(client)
		return nil, call.err
	}
	// Double-check under lock: another goroutine may have inserted meanwhile.
	if m.pool[key] == nil {
		m.pool[key] = map[string]*entry{}
	}
	if e := m.pool[key][serverName]; e != nil && e.client != nil {
		e.usedAt = time.Now()
		if e.client != client {
			closeClientBestEffort(client)
		}
		managed := m.ensureManagedLocked(key, serverName, e)
		call.entry = e
		close(call.ready)
		return managed, nil
	}
	e := &entry{capacityHeld: reservedCapacity, resourceReceiver: receiver, client: client, usedAt: time.Now(), epoch: call.epoch, configFingerprint: fingerprint, credentialFingerprint: credentialFingerprint, aliases: map[string]bool{requestedName: true, serverName: true}}
	e.managed = &managedClient{mgr: m, key: key, serverName: serverName, entry: e}
	m.pool[key][serverName] = e
	capacityTransferred = true
	call.entry = e
	close(call.ready)
	summary := m.poolSummaryLogLinesLocked(time.Now())
	logPoolSummary(summary)
	return e.managed, nil
}

func (m *Manager) newClient(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	if m.prov == nil {
		return nil, errors.New("mcp manager: provider not configured")
	}
	opts, err := m.prov.Options(ctx, serverName)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		return nil, errors.New("mcp manager: nil client options")
	}
	name := resolvedServiceName(opts, serverName)
	if usesUserTransportAuth(opts) {
		ctx = m.withAuthTokenContext(ctx, name, opts)
	}
	return m.newClientWithOptions(ctx, convID, name, opts)
}
func (m *Manager) newClientWithOptions(ctx context.Context, convID, serverName string, opts *mcpcfg.MCPClient) (mcpclient.Interface, error) {
	if err := opts.ValidateExecutionLocation(); err != nil {
		return nil, err
	}
	if opts.IsBrowserExecution() {
		return nil, ErrBrowserExecutionRequired
	}
	if opts.ClientOptions == nil {
		return nil, errors.New("mcp manager: missing client options")
	}
	// Runtime injection and SDK negotiation mutate their options. Own those
	// structs while preserving opaque transports, jars, stores and resolvers.
	owned := *opts
	clientOptions := *opts.ClientOptions
	owned.ClientOptions = &clientOptions
	if clientOptions.Auth != nil {
		auth := *clientOptions.Auth
		clientOptions.Auth = &auth
	}
	opts = &owned
	// Select per-request jar (provider beats static) and merge provider cookies.json into it
	// (if both present) before override,
	// so the very first POST can carry previously minted session cookies.
	var effectiveJar http.CookieJar
	if m.jarProvider != nil {
		var jerr error
		effectiveJar, jerr = m.jarProvider(ctx)
		if jerr != nil {
			return nil, fmt.Errorf("cookie jar provider: %w", jerr)
		}
	} else {
		effectiveJar = m.cookieJar
	}
	if effectiveJar != nil && opts.ClientOptions != nil {
		// Determine origin from transport URL
		origin := strings.TrimSpace(opts.ClientOptions.Transport.URL)
		if origin != "" {
			if u, perr := url.Parse(origin); perr == nil {
				if pj := opts.ClientOptions.CookieJar; pj != nil && pj != effectiveJar {
					if cs := pj.Cookies(u); len(cs) > 0 {
						effectiveJar.SetCookies(u, cs)
					}
				}
			}
		}
		// Override CookieJar with selected jar to ensure reuse across conversations
		opts.ClientOptions.CookieJar = effectiveJar
	}
	handler := m.newHandler
	if handler == nil {
		handler = func() protoclient.Handler { return nil }
	}
	h := handler()
	if sensitive, _ := ctx.Value(sensitiveSessionKey{}).(bool); sensitive {
		fence, ok := h.(interface{ BeginSensitivePayload() func() })
		if !ok {
			return nil, fmt.Errorf("artifact dispatch callback fence unavailable")
		}
		fence.BeginSensitivePayload() // session-owned: released only by session destruction
	}
	h = &changeHandler{Handler: h, manager: m, server: serverName, receiver: m.resourceReceiver(ctx), publicResources: m.IsToolsListPublic(ctx, serverName), subscriptionAck: make(chan schema.SubscriptionsAcknowledgedNotificationParams, 1)}
	// If handler supports setting conversation id, assign it.
	if ca, ok := h.(interface{ SetConversationID(string) }); ok {
		ca.SetConversationID(convID)
	}
	// Delegated OAuth (auth.mode=oauth with providerRef/inlineProvider):
	// viant/mcp owns transport-level coordination through the installed
	// external resolver, and the legacy BFF/browser transport paths are
	// disabled for this server. The selection is mutually exclusive with the
	// legacy transport-authorizer wiring below and is decided here, when the
	// configuration is compiled — there is no state where both paths can
	// attach credentials.
	// Challenge-mode delegated servers (auth.mode=oauth without an explicit
	// providerRef/inline provider) may resolve eagerly through a previously
	// validated learned binding; explicit configuration is never touched.
	if opts.ClientOptions.Auth != nil && opts.ClientOptions.Auth.IsDelegated() {
		m.applyLearnedBinding(ctx, serverName, opts.ClientOptions.Transport.URL, opts.ClientOptions.Auth)
	}
	delegated := opts.IsDelegatedAuth()
	if delegated {
		if opts.DisableDelegatedAuth {
			return nil, fmt.Errorf("provider_disabled: delegated auth for mcp server %q is disabled by configuration", serverName)
		}
		if m.credResolver == nil {
			return nil, fmt.Errorf("mcp server %q requires delegated oauth but no credential resolver is installed", serverName)
		}
		// Backward-compatible legacy flag handling must run before viant/mcp
		// compiles the requirement from this configuration.
		if err := opts.NormalizeDelegatedAuth(); err != nil {
			return nil, fmt.Errorf("mcp server %q: %w", serverName, err)
		}
		if err := opts.ClientOptions.Auth.Validate(); err != nil {
			return nil, err
		}
		opts.ClientOptions.Auth.ExternalResolver = catalogCredentialResolver{m.credResolver}
		if opts.ClientOptions.Auth.ProviderRegistry == nil {
			opts.ClientOptions.Auth.ProviderRegistry = m.providerRegistry
		}
		// A providerRef can only be validated and resolved through a registry;
		// compiling the requirement without one would silently skip provider
		// and client validation, so fail fast instead.
		if strings.TrimSpace(opts.ClientOptions.Auth.ProviderRef) != "" && opts.ClientOptions.Auth.ProviderRegistry == nil {
			return nil, fmt.Errorf("mcp server %q: auth.providerRef %q requires an oauth provider registry, but none is installed", serverName, opts.ClientOptions.Auth.ProviderRef)
		}
		// Register a validated inline provider in the runtime overlay (keyed
		// by its stable id) so credential storage and the background refresh
		// watcher can route to it. Registry-file providers win; a conflicting
		// definition fails client creation.
		if inline := opts.ClientOptions.Auth.InlineProvider; inline != nil {
			if registrar, ok := opts.ClientOptions.Auth.ProviderRegistry.(inlineProviderRegistrar); ok {
				if err := registrar.RegisterInline(ctx, inline); err != nil {
					return nil, fmt.Errorf("mcp server %q: %w", serverName, err)
				}
			}
		}
		// Delegated tokens live in the encrypted canonical store owned by the
		// resolver; never attach the per-user file token store or the
		// workspace auth transport.
		opts.ClientOptions.Auth.Store = nil
		// One-time protected-resource metadata cross-check for explicitly
		// configured providers: unreachable metadata is tolerated, but
		// metadata disagreeing with explicit configuration is a configuration
		// error — never an opportunity to silently rewrite providerRef.
		if strings.TrimSpace(opts.ClientOptions.Auth.ProviderRef) != "" || opts.ClientOptions.Auth.InlineProvider != nil {
			issuer := ""
			if inline := opts.ClientOptions.Auth.InlineProvider; inline != nil {
				issuer = inline.Issuer
			} else if m.providerRegistry != nil {
				if provider, pErr := m.providerRegistry.ResolveProvider(ctx, strings.TrimSpace(opts.ClientOptions.Auth.ProviderRef)); pErr == nil && provider != nil {
					issuer = provider.Issuer
				}
			}
			resource := strings.TrimSpace(opts.ClientOptions.Auth.Resource)
			if resource == "" {
				resource = strings.TrimSpace(opts.ClientOptions.Transport.URL)
			}
			if err := m.crossCheckExplicitMetadata(ctx, serverName, opts.ClientOptions.Transport.URL, issuer, resource, opts.ProtectedResourceMetadataURL); err != nil {
				return nil, err
			}
		}
	}
	if delegated {
		if err := m.configureDelegatedHTTPTransport(ctx, serverName, opts.ClientOptions); err != nil {
			return nil, err
		}
	}
	// Resolve per-user auth RoundTripper.
	var rt *authtransport.RoundTripper
	if !delegated {
		if m.authRTProvider != nil {
			rt = m.authRTProvider(ctx)
		}
		if rt == nil {
			rt = m.authRT
		}
	}
	useTransportAuth := usesUserTransportAuth(opts)
	// Only inject the auth RT into the HTTP transport when the MCP config
	// explicitly has auth settings. For auth:null configs, the token is
	// passed via the JSON-RPC interceptor (per-request) because
	// mcp.NewClient uses context.Background() for Initialize() which has
	// no user token.
	hasExplicitAuth := opts.ClientOptions.Auth != nil
	if rt != nil && hasExplicitAuth && useTransportAuth {
		opts.ClientOptions.SetAuthTransport(rt, &http.Client{Transport: observingRoundTripper{base: rt, serverName: serverName}, Jar: effectiveJar})
	}
	var clientRT *authtransport.RoundTripper
	if useTransportAuth {
		clientRT = rt
	}
	// Tool-surface discovery is deliberately bounded by the caller.  Using
	// NewClient here discards that deadline because it negotiates with a
	// background context, allowing one unavailable MCP endpoint to stall every
	// agent iteration for minutes.  Preserve cancellation through protocol
	// negotiation so best-effort discovery can fail fast and use its cooldown.
	cli, err := mcp.NewClientWithContext(ctx, h, opts.ClientOptions)
	if err != nil {
		// Surface delegated link-required failures as the stable Agently
		// typed error, preserving issuer/resource/metadata URL.
		wrapped := err
		// Persist a validated challenge-learned binding for challenge-mode
		// delegated servers so the next attempt resolves eagerly. Explicitly
		// configured servers never learn.
		if typed, ok := LinkRequired(wrapped); ok && typed.MetadataURL != "" &&
			delegated && strings.TrimSpace(opts.ClientOptions.Auth.ProviderRef) == "" && opts.ClientOptions.Auth.InlineProvider == nil {
			m.learnAuthBinding(ctx, serverName, opts.ClientOptions.Transport.URL, typed.Issuer, typed.Resource, typed.MetadataURL, typed.Scopes)
		}
		return nil, wrapped
	}
	// Attach the MCP-level auth interceptor for per-request token injection
	// and protocol-level 401 retries. Delegated servers never receive it:
	// the viant/mcp delegated transport is the single owner of token
	// injection and 401 recovery there.
	if clientRT != nil && !delegated {
		authorizer := auth.NewAuthorizer(clientRT)
		mcpclient.WithAuthInterceptor(authorizer)(cli)
	}
	fence, _ := h.(interface{ BeginSensitivePayload() func() })
	wrapped := &sensitiveClient{Client: cli, callback: fence}
	if handler, ok := h.(*changeHandler); ok {
		if err := wrapped.startResourceSubscription(ctx, handler, convID); err != nil {
			wrapped.Close()
			return nil, err
		}
	}
	return wrapped, nil
}

func closeClientBestEffort(client mcpclient.Interface) {
	if client == nil {
		return
	}
	if c, ok := client.(interface{ Close() error }); ok {
		_ = c.Close()
		return
	}
	if c, ok := client.(interface{ Close() }); ok {
		c.Close()
		return
	}
	if s, ok := client.(interface{ Shutdown(context.Context) error }); ok {
		_ = s.Shutdown(context.Background())
		return
	}
}

func (m *Manager) ensureManagedLocked(key, serverName string, e *entry) mcpclient.Interface {
	if e == nil {
		return nil
	}
	if e.managed == nil {
		e.managed = &managedClient{mgr: m, key: key, serverName: serverName, entry: e}
	}
	return e.managed
}

func (m *Manager) beginUse(e *entry) (mcpclient.Interface, error) {
	if m == nil || e == nil {
		return nil, errors.New("mcp manager: nil managed client")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.evicted || e.closed || e.client == nil {
		return nil, errors.New("mcp manager: client is closed")
	}
	e.active++
	return e.client, nil
}

func (m *Manager) endUse(e *entry) {
	if m == nil || e == nil {
		return
	}
	var toClose mcpclient.Interface
	m.mu.Lock()
	if e.active > 0 {
		e.active--
	}
	if e.active == 0 {
		e.usedAt = time.Now()
		if e.evicted && !e.closed {
			e.closed = true
			toClose = m.retirementClientLocked(e)
		}
	}
	m.mu.Unlock()
	closeClientBestEffort(toClose)
}

func (m *Manager) evictEntryLocked(key, serverName string, e *entry, reasons ...ClientEvictionReason) mcpclient.Interface {
	if e == nil {
		return nil
	}
	if !e.evicted && len(m.clientEvictionListeners) > 0 {
		reason := ClientRetired
		if len(reasons) > 0 {
			reason = reasons[0]
		}
		m.pendingClientEvictions = append(m.pendingClientEvictions, ClientEviction{Server: serverName, Client: e.managed, Reason: reason})
	}
	m.invalidateToolCatalogLocked(serverName)
	e.evicted = true
	if perServer := m.pool[key]; perServer != nil {
		delete(perServer, serverName)
		if len(perServer) == 0 {
			delete(m.pool, key)
		}
	}
	if e.active == 0 && !e.closed {
		e.closed = true
		return m.retirementClientLocked(e)
	}
	return nil
}

type poolServerSummary struct {
	server       string
	total        int
	background   int
	conversation int
	syntheticSeq int
	other        int
}

type poolClientSummary struct {
	server    string
	poolKey   string
	scopeType string
	active    int
	usedAt    time.Time
}

func (m *Manager) poolSummaryLogLinesLocked(now time.Time) []string {
	if m == nil || m.poolSummaryEvery <= 0 {
		return nil
	}
	if !m.poolSummaryAt.IsZero() && now.Sub(m.poolSummaryAt) < m.poolSummaryEvery {
		return nil
	}
	m.poolSummaryAt = now

	byServer := map[string]*poolServerSummary{}
	clients := []poolClientSummary{}
	poolKeys := 0
	totalClients := 0
	for poolKey, perServer := range m.pool {
		if len(perServer) == 0 {
			continue
		}
		poolKeys++
		for serverName, e := range perServer {
			if e == nil || e.client == nil {
				continue
			}
			serverName = strings.TrimSpace(serverName)
			if serverName == "" {
				serverName = "unknown"
			}
			summary := byServer[serverName]
			if summary == nil {
				summary = &poolServerSummary{server: serverName}
				byServer[serverName] = summary
			}
			totalClients++
			summary.total++
			scopeType := poolScopeType(poolKey, serverName)
			switch scopeType {
			case "background":
				summary.background++
			case "synthetic_seq":
				summary.syntheticSeq++
			case "conversation":
				summary.conversation++
			default:
				summary.other++
			}
			clients = append(clients, poolClientSummary{
				server:    serverName,
				poolKey:   poolKey,
				scopeType: scopeType,
				active:    e.active,
				usedAt:    e.usedAt,
			})
		}
	}

	servers := make([]string, 0, len(byServer))
	for server := range byServer {
		servers = append(servers, server)
	}
	sort.Strings(servers)

	lines := []string{fmt.Sprintf("[info][mcp-client-pool] summary total=%d pool_keys=%d servers=%d", totalClients, poolKeys, len(servers))}
	for _, server := range servers {
		summary := byServer[server]
		lines = append(lines, fmt.Sprintf("[info][mcp-client-pool] server=%q total=%d background=%d conversation=%d synthetic_seq=%d other=%d",
			summary.server, summary.total, summary.background, summary.conversation, summary.syntheticSeq, summary.other))
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].server != clients[j].server {
			return clients[i].server < clients[j].server
		}
		return clients[i].poolKey < clients[j].poolKey
	})
	for _, client := range clients {
		lines = append(lines, fmt.Sprintf("[info][mcp-client-pool] client server=%q pool_key=%q scope_type=%s active=%d used_at=%s",
			client.server, client.poolKey, client.scopeType, client.active, client.usedAt.Format(time.RFC3339Nano)))
	}
	return lines
}

func logPoolSummary(lines []string) {
	for _, line := range lines {
		log.Print(line)
	}
}

func poolScopeType(poolKey, server string) string {
	poolKey = strings.TrimSpace(poolKey)
	server = strings.TrimSpace(server)
	if poolKey == "" {
		return "other"
	}
	discoveryPrefix := "mcp-discovery:" + server + ":"
	if idx := strings.Index(poolKey, discoveryPrefix); idx >= 0 {
		suffix := strings.TrimSpace(poolKey[idx+len(discoveryPrefix):])
		if suffix == "background" {
			return "background"
		}
		if suffix == "public" {
			return "public"
		}
		if strings.HasPrefix(suffix, "principal:") {
			return "principal"
		}
		if suffix != "" {
			return "synthetic_seq"
		}
		return "other"
	}
	return "conversation"
}

// Touch updates last-used time for (convID, serverName).
func (m *Manager) Touch(convID, serverName string) {
	m.mu.Lock()
	// Touch is called without context, so search all pool keys that end with the convID.
	for key, perServer := range m.pool {
		if key == convID || strings.HasSuffix(key, ":"+convID) {
			if e := perServer[serverName]; e != nil {
				e.usedAt = time.Now()
			}
		}
	}
	m.mu.Unlock()
}

// CloseConversation drops all clients for a conversation (across all users).
// Note: underlying transports may keep connections if the library doesn't expose Close.
func (m *Manager) CloseConversation(convID string) {
	defer m.dispatchClientEvictions()
	var toClose []mcpclient.Interface
	m.mu.Lock()
	if m.epoch == nil {
		m.epoch = map[string]uint64{}
	}
	for key, perServer := range m.pool {
		if key == convID || strings.HasSuffix(key, ":"+convID) {
			for server, e := range perServer {
				if client := m.evictEntryLocked(key, server, e); client != nil {
					toClose = append(toClose, client)
				}
			}
			m.epoch[key]++
		}
	}
	for key := range m.inflight {
		if key == convID || strings.HasSuffix(key, ":"+convID) {
			m.epoch[key]++
		}
	}
	m.mu.Unlock()
	for _, client := range toClose {
		closeClientBestEffort(client)
	}
}

// EvictUserServer drops every pooled client for one user and MCP server.
// Called after a delegated credential change (link, disconnect,
// invalid_grant) so the next use resolves the new credential instead of
// reusing a client authenticated with the old one. It never touches other
// users' entries, sessions or auth context.
func (m *Manager) EvictUserServer(userID, serverName string) {
	defer m.dispatchClientEvictions()
	userID = strings.TrimSpace(userID)
	serverName = strings.TrimSpace(serverName)
	if m == nil || userID == "" || serverName == "" {
		return
	}
	// Default authenticated pools encode the exact owner length; matching the
	// raw user prefix alone misses them and must not confuse colon subjects.
	principalPrefix := fmt.Sprintf("principal:%d:%s:", len(userID), userID)
	var toClose []mcpclient.Interface
	m.mu.Lock()
	if m.epoch == nil {
		m.epoch = map[string]uint64{}
	}
	for key, perServer := range m.pool {
		if key != userID && !strings.HasPrefix(key, userID+":") && !strings.HasPrefix(key, principalPrefix) {
			continue
		}
		if e := perServer[serverName]; e != nil {
			if client := m.evictEntryLocked(key, serverName, e); client != nil {
				toClose = append(toClose, client)
			}
			m.epoch[key]++
		}
	}
	m.mu.Unlock()
	for _, client := range toClose {
		closeClientBestEffort(client)
	}
}

// Reap closes idle clients beyond TTL by dropping references.
func (m *Manager) Reap() {
	defer m.dispatchClientEvictions()
	cutoff := time.Now().Add(-m.ttl)
	var toClose []mcpclient.Interface
	m.mu.Lock()
	for convID, perServer := range m.pool {
		for server, e := range perServer {
			if e == nil {
				delete(perServer, server)
				continue
			}
			if e.active == 0 && e.usedAt.Before(cutoff) {
				if client := m.evictEntryLocked(convID, server, e, ClientIdleEvicted); client != nil {
					toClose = append(toClose, client)
				}
			}
		}
		if len(perServer) == 0 {
			delete(m.pool, convID)
		}
	}
	m.mu.Unlock()
	for _, client := range toClose {
		closeClientBestEffort(client)
	}
}

// Reconnect drops the cached client for (convID, serverName) and creates a new one.
// It returns the fresh client or an error if recreation fails.
func (m *Manager) Reconnect(ctx context.Context, convID, serverName string) (mcpclient.Interface, error) {
	defer m.dispatchClientEvictions()
	if m == nil {
		return nil, errors.New("mcp manager: nil manager")
	}
	key := m.poolKey(ctx, convID)
	// Drop existing entry to force re-creation
	var toClose mcpclient.Interface
	m.mu.Lock()
	m.invalidateToolCatalogLocked(serverName)
	if m.pool[key] != nil {
		if e := m.pool[key][serverName]; e != nil && e.client != nil {
			toClose = m.evictEntryLocked(key, serverName, e)
		}
	}
	summary := m.poolSummaryLogLinesLocked(time.Now())
	m.mu.Unlock()
	m.notifyCatalogChange(serverName)
	scopeType := poolScopeType(key, serverName)
	log.Printf("[warn][mcp-client-pool] reconnect server=%q scope_type=%s conv_id=%q pool_key=%q closing_existing=%v",
		serverName, scopeType, convID, key, toClose != nil)
	logPoolSummary(summary)
	closeClientBestEffort(toClose)
	// Recreate
	client, err := m.Get(ctx, convID, serverName)
	if err != nil {
		log.Printf("[warn][mcp-client-pool] reconnect failed server=%q scope_type=%s conv_id=%q pool_key=%q err=%v",
			serverName, scopeType, convID, key, err)
		return nil, err
	}
	log.Printf("[info][mcp-client-pool] reconnect succeeded server=%q scope_type=%s conv_id=%q pool_key=%q",
		serverName, scopeType, convID, key)
	return client, nil
}

// StartReaper launches a background goroutine that periodically invokes Reap
// until the provided context is cancelled or the returned stop function is
// called. If interval is non-positive, ttl/2 is used with a minimum of 1 minute.
func (m *Manager) StartReaper(ctx context.Context, interval time.Duration) (stop func()) {
	min := time.Minute
	if interval <= 0 {
		interval = m.ttl / 2
		if interval < min {
			interval = min
		}
	}
	done := make(chan struct{})
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.Reap()
			case <-ctx.Done():
				return
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// CatalogNamesExcluding enumerates configured discovery roots while omitting
// trusted, separately supplied metadata sources before option preparation. It
// does not initialize clients or promote ordinary tool connections to roots.
// Process-owned registrations retain the normal configured-name collision rule.
func (m *Manager) CatalogNamesExcluding(ctx context.Context, excluded []string) ([]string, error) {
	if m == nil || ctx == nil || ctx.Err() != nil {
		return nil, errors.New("mcp catalog inventory unavailable")
	}
	skip := map[string]bool{}
	for _, name := range excluded {
		skip[name] = true
	}
	var names []string
	var err error
	if lister, ok := m.prov.(interface {
		CatalogNames(context.Context) ([]string, error)
	}); ok {
		names, err = lister.CatalogNames(ctx)
	} else if lister, ok := m.prov.(providerLister); ok {
		names, err = lister.Names(ctx)
	} else if m.prov != nil {
		return nil, errors.New("mcp provider does not expose connection inventory")
	}
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if m.IsLocalClient(name) {
			return nil, fmt.Errorf("mcp local connection collides with configured provider %q", name)
		}
		if !skip[name] {
			filtered = append(filtered, name)
		}
	}
	return m.mergeInventoryNames(ctx, filtered)
}
