// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	toolcatalog "github.com/viant/mcp/client/catalog"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	"github.com/viant/mcp/client/observe"
	"github.com/viant/mcp/client/toolname"
)

// WithPersistentToolCatalog lets the application retain scoped tools/list
// metadata until notification, configuration/connection invalidation or manager
// lifetime ends, regardless of producer TTL. Retained memory follows that
// lifetime rather than the legacy page-count cap. It changes no tool invocation or
// credential fence. Without this option the producer/legacy TTL policy remains.
func WithPersistentToolCatalog() Option {
	return func(m *Manager) error { m.persistentToolCatalog = true; return nil }
}

// Only tools/list metadata is retained. Each generation owns one decoded copy;
// callers receive detached objects so they cannot mutate shared metadata.
type toolCatalogPage struct {
	server                         string
	public                         bool
	catalogKey, cursor, nextCursor string
	ready                          chan struct{}
	content                        *toolCatalogContent
	expires                        time.Time
	revision                       [16]byte
	names                          []string
}

type toolCatalogContent = toolcatalog.Page

func prepareToolCatalogContent(wire []byte, servers ...string) (*toolCatalogContent, error) {
	return toolcatalog.New(wire, servers...)
}

// Alias names are trusted connection metadata captured when the page arrives.
func (c *managedClient) catalogAliases() []string {
	c.mgr.mu.Lock()
	defer c.mgr.mu.Unlock()
	names := []string{c.serverName}
	if c.entry != nil {
		for name := range c.entry.aliases {
			if name != c.serverName {
				names = append(names, name)
			}
		}
	}
	return names
}

func (p *toolCatalogPage) reusable(now time.Time) bool {
	return p.expires.IsZero() || p.expires.After(now)
}

type toolCatalogReadSnapshotKey struct{}
type toolCatalogReadSnapshot struct {
	pages        sync.Map
	noRetryPages sync.Map
}

// NoRetry pages are held only by the explicit read snapshot. They never enter
// the manager's retained catalog, including when the producer advertises TTL.
type noRetryToolCatalogPage struct {
	ready   chan struct{}
	content *toolCatalogContent
	err     error
}

// WithToolCatalogReadSnapshot reuses one authenticated tools/list generation
// while assembling a single tool surface. Explicit ttlMs=0 still prevents
// reuse by later independent requests unless the manager explicitly owns
// persistent catalog retention.
func WithToolCatalogReadSnapshot(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if existing, _ := ctx.Value(toolCatalogReadSnapshotKey{}).(*toolCatalogReadSnapshot); existing != nil {
		return ctx
	}
	return WithNewToolCatalogReadSnapshot(ctx)
}

// WithNewToolCatalogReadSnapshot begins a distinct read even when an outer
// metadata operation already has a snapshot. A new approved datasource fetch
// must not inherit another fetch's zero-TTL metadata.
func WithNewToolCatalogReadSnapshot(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolCatalogReadSnapshotKey{}, &toolCatalogReadSnapshot{})
}

func (m *Manager) invalidateToolCatalogLocked(server string) {
	if m.toolCatalogGeneration == nil {
		m.toolCatalogGeneration = map[string]uint64{}
	}
	m.toolCatalogGeneration[server]++
	// A tool-list change only invalidates snapshots that depend on this MCP.
	// A snapshot with no source tags is conservatively invalidated because its
	// dependency set is unknown. Other providers may reconnect while a local
	// resource catalog is being populated.
	for key, held := range m.visibleCatalogs {
		if held == nil || held.sources == nil || len(held.sources) == 0 || held.sources[server] {
			delete(m.visibleCatalogs, key)
		}
	}
	for key, page := range m.toolCatalog {
		if page.server == server {
			delete(m.toolCatalog, key)
		}
	}
}

func (c *managedClient) listTools(ctx context.Context, client mcpclient.Interface, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	m := c.mgr
	if snapshot, _ := ctx.Value(toolCatalogReadSnapshotKey{}).(*toolCatalogReadSnapshot); snapshot != nil && mcpclient.NewRequestOptions(options).NoRetry {
		return c.listToolsNoRetrySnapshot(ctx, client, cursor, snapshot, options...)
	}
	ctx, raw, scope, witness, delegatedToken, cacheable, err := c.toolCatalogScope(ctx, options...)
	if err != nil {
		return nil, err
	}
	if !cacheable {
		return c.fetchToolCatalog(ctx, client, cursor, observe.CacheBypass, options...)
	}
	readSnapshot, _ := ctx.Value(toolCatalogReadSnapshotKey{}).(*toolCatalogReadSnapshot)
	pageCursor := "first"
	if cursor != nil {
		pageCursor = "cursor:" + *cursor
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		generation := m.toolCatalogGeneration[c.serverName]
		catalogKey := fmt.Sprintf("%s:%s:%x:%d:", c.serverName, scope, sha256.Sum256(raw), generation)
		key := catalogKey + pageCursor
		snapshotTiming := observe.Start(ctx, "mcp.tools.list.read_snapshot", observe.Labels{Provider: c.serverName})
		if readSnapshot == nil {
			snapshotTiming.End(observe.OK, observe.CacheBypass)
		} else {
			if cached, found := readSnapshot.pages.Load(key); found {
				snapshotTiming.End(observe.OK, observe.CacheHit)
				m.mu.Unlock()
				return cached.(*toolCatalogContent).Copy()
			}
			snapshotTiming.End(observe.OK, observe.CacheMiss)
		}
		if m.toolCatalog == nil {
			m.toolCatalog = map[string]*toolCatalogPage{}
		}
		if cached := m.toolCatalog[key]; cached != nil {
			if cached.ready != nil {
				ready := cached.ready
				m.mu.Unlock()
				select {
				case <-ready:
					// This caller already joined the source operation. A zero-TTL
					// result is not retained in m.toolCatalog, but its completed
					// bytes may satisfy these existing waiters under the same
					// credential, configuration and notification generation.
					_, currentRaw, currentScope, _, _, currentCacheable, scopeErr := c.toolCatalogScope(ctx, options...)
					if scopeErr != nil {
						return nil, scopeErr
					}
					if !bytes.Equal(currentRaw, raw) {
						return nil, fmt.Errorf("MCP tool catalog configuration changed during source read")
					}
					if !currentCacheable || currentScope != scope {
						return c.listTools(ctx, client, cursor, options...)
					}
					m.mu.Lock()
					payload := cached.content
					current := generation == m.toolCatalogGeneration[c.serverName] && cached.reusable(time.Now())
					m.mu.Unlock()
					if payload != nil && current {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
						if readSnapshot != nil {
							readSnapshot.pages.Store(key, payload)
						}
						return payload.Copy()
					}
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if cached.reusable(time.Now()) {
				content := cached.content
				m.mu.Unlock()
				return content.Copy()
			}
			delete(m.toolCatalog, key)
		}
		// Bound legacy retained metadata; persistent retention is application-owned.
		// Pending requests remain valid and their results
		// are published only if their slot and generation are still current.
		for k, v := range m.toolCatalog {
			if v.ready == nil && !v.reusable(time.Now()) {
				delete(m.toolCatalog, k)
			}
		}
		if !m.persistentToolCatalog && len(m.toolCatalog) >= 512 {
			m.mu.Unlock()
			return c.fetchToolCatalog(ctx, client, cursor, observe.CacheBypass, options...)
		}
		pending := &toolCatalogPage{server: c.serverName, catalogKey: catalogKey, cursor: pageCursor, ready: make(chan struct{})}
		m.toolCatalog[key] = pending
		m.mu.Unlock()
		result, err := c.fetchToolCatalog(ctx, client, cursor, observe.CacheMiss, options...)
		_, currentRaw, currentScope, _, _, currentCacheable, scopeErr := c.toolCatalogScope(ctx, options...)
		validCredential := scopeErr == nil && currentCacheable && currentScope == scope && bytes.Equal(currentRaw, raw)
		if witness != nil {
			witness.mu.Lock()
			validCredential = validCredential && witness.observed && witness.token == delegatedToken
			witness.mu.Unlock()
		}
		var bytes []byte
		var cacheErr error
		var content *toolCatalogContent
		if err == nil && result != nil {
			bytes, cacheErr = json.Marshal(result)
			if cacheErr == nil {
				content, cacheErr = prepareToolCatalogContent(bytes, c.catalogAliases()...)
			}
			if cacheErr == nil {
				_, cacheErr = rand.Read(pending.revision[:])
			}
		}
		m.mu.Lock()
		if result != nil && result.CacheScope == mcpschema.ListToolsResultCacheScopePrivate {
			if m.protectedToolCatalog == nil {
				m.protectedToolCatalog = map[string][32]byte{}
			}
			m.protectedToolCatalog[c.serverName] = sha256.Sum256(raw)
		}
		publishable := m.toolCatalog[key] == pending && validCredential && cacheErr == nil && err == nil && result != nil && ctx.Err() == nil && generation == m.toolCatalogGeneration[c.serverName] && !(scope == "public" && result.CacheScope == mcpschema.ListToolsResultCacheScopePrivate)
		if publishable {
			// The pending object is held only by callers already waiting on it.
			// Legacy zero-TTL results do not create a retained entry; an explicit
			// application retention option keeps them in this existing catalog.
			pending.content = content
		}
		if m.toolCatalog[key] == pending {
			if publishable && (m.persistentToolCatalog || !(result.CacheScope != "" && result.TtlMs == 0)) {
				pending.public = scope == "public"
				if result.NextCursor != nil && *result.NextCursor != "" {
					pending.nextCursor = "cursor:" + *result.NextCursor
				}
				pending.names = make([]string, len(result.Tools))
				for i, tool := range result.Tools {
					pending.names[i] = tool.Name
				}
				if !m.persistentToolCatalog {
					ttl := 5 * time.Minute
					if result.TtlMs > 0 && time.Duration(result.TtlMs)*time.Millisecond < ttl {
						ttl = time.Duration(result.TtlMs) * time.Millisecond
					}
					pending.expires = time.Now().Add(ttl)
				}
			} else {
				delete(m.toolCatalog, key)
			}
		}
		if readSnapshot != nil && validCredential && cacheErr == nil && err == nil && result != nil && ctx.Err() == nil && generation == m.toolCatalogGeneration[c.serverName] {
			readSnapshot.pages.Store(key, content)
		}
		close(pending.ready)
		pending.ready = nil
		m.mu.Unlock()
		return result, err
	}
}

// listToolsNoRetrySnapshot coalesces only one explicit operation's identical
// metadata reads. Its source call still carries WithNoRetry, so a lost session
// or 401 is never replayed. Ordinary NoRetry calls keep their force-fresh path.
func (c *managedClient) listToolsNoRetrySnapshot(ctx context.Context, client mcpclient.Interface, cursor *string, snapshot *toolCatalogReadSnapshot, options ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	ctx, raw, scope, witness, delegatedToken, cacheable, err := c.toolCatalogScopeWithNoRetrySnapshot(ctx, options...)
	if err != nil {
		return nil, err
	}
	if !cacheable || scope == "public" {
		return c.fetchToolCatalog(ctx, client, cursor, observe.CacheBypass, options...)
	}
	pageCursor := "first"
	if cursor != nil {
		pageCursor = "cursor:" + *cursor
	}
	m := c.mgr
	m.mu.Lock()
	generation := m.toolCatalogGeneration[c.serverName]
	m.mu.Unlock()
	key := fmt.Sprintf("noretry:%s:%s:%x:%d:%s", c.serverName, scope, sha256.Sum256(raw), generation, pageCursor)
	pending := &noRetryToolCatalogPage{ready: make(chan struct{})}
	snapshotTiming := observe.Start(ctx, "mcp.tools.list.read_snapshot", observe.Labels{Provider: c.serverName})
	actual, loaded := snapshot.noRetryPages.LoadOrStore(key, pending)
	page := actual.(*noRetryToolCatalogPage)
	if !loaded {
		snapshotTiming.End(observe.OK, observe.CacheMiss)
	} else {
		select {
		case <-page.ready:
			snapshotTiming.End(observe.OK, observe.CacheHit)
		default:
			snapshotTiming.End(observe.OK, observe.CacheMiss)
		}
	}
	if !loaded {
		result, fetchErr := c.fetchToolCatalog(ctx, client, cursor, observe.CacheMiss, options...)
		var encoded []byte
		if fetchErr == nil && result != nil {
			encoded, fetchErr = json.Marshal(result)
		}
		if fetchErr == nil && result == nil {
			fetchErr = fmt.Errorf("MCP tool catalog source returned no result")
		}
		_, freshRaw, freshScope, _, _, freshCacheable, scopeErr := c.toolCatalogScopeWithNoRetrySnapshot(ctx, options...)
		if fetchErr == nil && (scopeErr != nil || !freshCacheable || freshScope != scope || !bytes.Equal(freshRaw, raw)) {
			fetchErr = fmt.Errorf("MCP tool catalog authority changed during no-retry source read")
		}
		if witness != nil {
			witness.mu.Lock()
			observed := witness.observed && witness.token == delegatedToken
			witness.mu.Unlock()
			if fetchErr == nil && !observed {
				fetchErr = fmt.Errorf("MCP tool catalog credential was not observed")
			}
		}
		m.mu.Lock()
		freshGeneration := m.toolCatalogGeneration[c.serverName]
		m.mu.Unlock()
		if fetchErr == nil && freshGeneration != generation {
			fetchErr = fmt.Errorf("MCP tool catalog changed during no-retry source read")
		}
		if fetchErr == nil && ctx.Err() != nil {
			fetchErr = ctx.Err()
		}
		if fetchErr == nil {
			page.content, fetchErr = prepareToolCatalogContent(encoded, c.catalogAliases()...)
		}
		page.err = fetchErr
		if page.err != nil {
			snapshot.noRetryPages.CompareAndDelete(key, page)
		}
		close(page.ready)
	}
	select {
	case <-page.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	currentCtx, currentRaw, currentScope, _, _, currentCacheable, scopeErr := c.toolCatalogScopeWithNoRetrySnapshot(ctx, options...)
	if scopeErr != nil {
		return nil, scopeErr
	}
	if !currentCacheable || currentScope != scope || !bytes.Equal(currentRaw, raw) {
		return nil, fmt.Errorf("MCP tool catalog authority changed during no-retry read")
	}
	if err := currentCtx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	currentGeneration := m.toolCatalogGeneration[c.serverName]
	m.mu.Unlock()
	if currentGeneration != generation {
		return nil, fmt.Errorf("MCP tool catalog changed during no-retry read")
	}
	if page.err != nil {
		return nil, page.err
	}
	return page.content.Copy()
}

// IsToolsListPublic reports whether configured catalog metadata can be shared
// without invoking an authorization context. Unknown/custom transports remain
// principal-scoped even when their workspace principal is anonymous.
func (m *Manager) IsToolsListPublic(ctx context.Context, server string) bool {
	cfg, err := m.Options(ctx, server)
	if err != nil || cfg == nil || cfg.ClientOptions == nil {
		return false
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return false
	}
	m.mu.Lock()
	protected, seen := m.protectedToolCatalog[server]
	m.mu.Unlock()
	if seen && protected == sha256.Sum256(raw) {
		return false
	}
	if !cfg.IsToolsListPublic() || cfg.Auth != nil || m.IsLocalClient(server) || m.newClientFn != nil || cfg.Transport.URL == "" {
		return false
	}
	endpoint, err := url.Parse(cfg.Transport.URL)
	if err != nil {
		return false
	}
	cookies, err := m.catalogCookies(ctx, endpoint, cfg)
	return err == nil && len(cookies) == 0
}

// toolCatalogScope is shared by population and read-only snapshot lookup, so
// consumers cannot invent a second credential or visibility policy.
func (c *managedClient) toolCatalogScope(ctx context.Context, options ...mcpclient.RequestOption) (context.Context, []byte, string, *catalogAuthUse, string, bool, error) {
	return c.toolCatalogScopeWithSnapshotMode(ctx, false, options...)
}

func (c *managedClient) toolCatalogScopeWithNoRetrySnapshot(ctx context.Context, options ...mcpclient.RequestOption) (context.Context, []byte, string, *catalogAuthUse, string, bool, error) {
	return c.toolCatalogScopeWithSnapshotMode(ctx, true, options...)
}

func (c *managedClient) toolCatalogScopeWithSnapshotMode(ctx context.Context, allowNoRetrySnapshot bool, options ...mcpclient.RequestOption) (context.Context, []byte, string, *catalogAuthUse, string, bool, error) {
	m := c.mgr
	cfg, err := m.Options(ctx, c.serverName)
	if err != nil {
		return ctx, nil, "", nil, "", false, err
	}
	// In-process handlers can inspect arbitrary context authority. HTTP cookies
	// are partitioned by the endpoint-specific transport witness below.
	if cfg == nil || cfg.ClientOptions == nil || m.IsLocalClient(c.serverName) {
		return ctx, nil, "", nil, "", false, nil
	}
	transportScope, transportCacheable, err := m.catalogTransportCredentialScope(ctx, c.serverName, cfg)
	if err != nil || !transportCacheable {
		return ctx, nil, "", nil, "", false, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ctx, nil, "", nil, "", false, nil
	}
	request := mcpclient.NewRequestOptions(options)
	// Request IDs and replay controls retain their normal transport semantics.
	if request.RequestId != nil || request.NoRetry && !allowNoRetrySnapshot || request.JsonrpcVersion != "" {
		return ctx, nil, "", nil, "", false, nil
	}
	token, _ := ctx.Value(authtransport.ContextAuthTokenKey).(string)
	delegatedToken := ""
	var witness *catalogAuthUse
	if cfg.IsDelegatedAuth() {
		if m.credResolver == nil {
			return ctx, nil, "", nil, "", false, nil
		}
		requirement, compileErr := cfg.Auth.CompileRequirement(ctx, c.serverName, cfg.Transport.URL)
		if compileErr != nil {
			return ctx, nil, "", nil, "", false, compileErr
		}
		resolved, resolveErr := m.credResolver.Resolve(ctx, *requirement)
		if resolveErr != nil {
			return ctx, nil, "", nil, "", false, resolveErr
		}
		if resolved == nil || resolved.Verify(requirement, time.Now()) != nil {
			return ctx, nil, "", nil, "", false, nil
		}
		delegatedToken = resolved.Token
		witness = &catalogAuthUse{}
		ctx = context.WithValue(ctx, catalogAuthKey{}, witness)
	}
	principal := authctx.EffectiveUserID(ctx)
	if m.userIDFn != nil {
		principal = m.userIDFn(ctx)
	}
	requestToken := request.StringToken
	if requestToken != "" && requestToken == token && m.newClientFn == nil && !cfg.IsDelegatedAuth() && !cfg.IsBrowserExecution() {
		switch cfg.Transport.Type {
		case "streamable", "sse", "http":
			if endpoint, err := url.Parse(cfg.Transport.URL); err == nil && (endpoint.Scheme == "http" || endpoint.Scheme == "https") {
				// For the ordinary HTTP client this explicit option only replaces
				// the same context Bearer value. Preserve the request itself;
				// normalize its redundant slot only in the existing metadata key.
				requestToken = ""
			}
		}
	}
	credential, _ := json.Marshal([]string{principal, delegatedToken, token, requestToken, authctx.MCPAuthToken(ctx, false), authctx.MCPAuthToken(ctx, true), authctx.Provider(ctx), authctx.EffectiveUserID(ctx), transportScope})
	scope := fmt.Sprintf("private:%p:%x", c.entry, sha256.Sum256(credential))
	// Explicit public visibility is necessary. Any configured authentication
	// or request credential keeps the page within its connection/principal.
	if m.IsToolsListPublic(ctx, c.serverName) && token == "" && request.StringToken == "" {
		scope = "public"
	}
	return ctx, raw, scope, witness, delegatedToken, true, nil
}

// ToolCatalogSnapshot returns an already cached, complete tools/list catalog,
// including a zero-TTL snapshot held by this operation or an explicitly
// persistent manager. Its opaque identity follows the selected retention policy
// and changes on
// publication, connection/configuration changes or invalidation. It never lists
// tools or stores a parallel catalog.
func (m *Manager) ToolCatalogSnapshot(ctx context.Context, conversation, server string, options ...mcpclient.RequestOption) (string, []mcpschema.Tool, error) {
	return m.cachedToolCatalogState(ctx, conversation, server, true, nil, options...)
}

// ToolCatalogSelect reads matching literal names or advertised aliases from the
// existing scoped generation. A complete catalog with no match still returns its
// identity. Metadata-only reads omit input/output schemas. No source is fetched.
func (m *Manager) ToolCatalogSelect(ctx context.Context, conversation, server string, names []string, metadataOnly bool, options ...mcpclient.RequestOption) (string, []mcpschema.Tool, error) {
	return m.cachedToolCatalogState(ctx, conversation, server, true, &toolCatalogSelection{names: names, metadataOnly: metadataOnly}, options...)
}

type toolCatalogSelection struct {
	names        []string
	metadataOnly bool
}

// ToolCatalogIdentity returns the same opaque identity without decoding schemas
// or initializing a missing connection.
func (m *Manager) ToolCatalogIdentity(ctx context.Context, conversation, server string, options ...mcpclient.RequestOption) (string, error) {
	identity, _, err := m.cachedToolCatalogState(ctx, conversation, server, false, nil, options...)
	return identity, err
}

func (m *Manager) cachedToolCatalogState(ctx context.Context, conversation, server string, includeTools bool, selection *toolCatalogSelection, options ...mcpclient.RequestOption) (string, []mcpschema.Tool, error) {
	if m == nil || ctx == nil {
		return "", nil, nil
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	configured, err := m.Options(ctx, server)
	if err != nil {
		return "", nil, err
	}
	fingerprint, err := effectiveConfigFingerprint(configured)
	if err != nil {
		return "", nil, err
	}
	key, service := m.poolKey(ctx, conversation), resolvedServiceName(configured, server)
	m.mu.Lock()
	held := m.pool[key][service]
	var managed *managedClient
	if held != nil && held.client != nil && !held.evicted && !held.closed && held.configFingerprint == fingerprint {
		managed = held.managed
	}
	m.mu.Unlock()
	if managed == nil {
		return "", nil, nil
	}
	identity, tools, err := managed.selectedToolCatalogState(ctx, includeTools, selection, options...)
	if err != nil {
		return "", nil, err
	}
	if !m.IsCurrentClient(ctx, conversation, server, managed) {
		return "", nil, nil
	}
	return identity, tools, nil
}

func (c *managedClient) toolCatalogSnapshot(ctx context.Context, options ...mcpclient.RequestOption) (string, []mcpschema.Tool, error) {
	return c.toolCatalogState(ctx, true, options...)
}

func (c *managedClient) toolCatalogState(ctx context.Context, includeTools bool, options ...mcpclient.RequestOption) (string, []mcpschema.Tool, error) {
	return c.selectedToolCatalogState(ctx, includeTools, nil, options...)
}

func (c *managedClient) selectedToolCatalogState(ctx context.Context, includeTools bool, selection *toolCatalogSelection, options ...mcpclient.RequestOption) (string, []mcpschema.Tool, error) {
	_, raw, scope, _, _, cacheable, err := c.toolCatalogScope(ctx, options...)
	if err != nil || !cacheable {
		return "", nil, err
	}
	readSnapshot, _ := ctx.Value(toolCatalogReadSnapshotKey{}).(*toolCatalogReadSnapshot)
	m := c.mgr
	identity, pages, err := func() (string, []*toolCatalogContent, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		cursor := "first"
		seen := map[string]bool{}
		var pages []*toolCatalogContent
		var revisions []string
		now := time.Now()
		for {
			if seen[cursor] {
				return "", nil, nil
			}
			seen[cursor] = true
			key := fmt.Sprintf("%s:%s:%x:%d:%s", c.serverName, scope, sha256.Sum256(raw), m.toolCatalogGeneration[c.serverName], cursor)
			var content *toolCatalogContent
			var revision, nextCursor string
			retained := false
			if page := m.toolCatalog[key]; page != nil && page.ready == nil && page.reusable(now) {
				content = page.content
				revision = fmt.Sprintf("%x", page.revision)
				nextCursor, retained = page.nextCursor, true
			} else if readSnapshot != nil {
				// Zero-TTL catalogs are reusable only within the current operation.
				// Connection, credentials and notification generation still fence the key.
				if held, found := readSnapshot.pages.Load(key); found {
					content = held.(*toolCatalogContent)
					revision = fmt.Sprintf("read:%x", content.Digest())
				}
			}
			if content == nil {
				return "", nil, nil
			}
			if includeTools {
				pages = append(pages, content)
			}
			if !retained && content.NextCursor() != "" {
				nextCursor = "cursor:" + content.NextCursor()
			}
			revisions = append(revisions, key+":"+revision)
			if nextCursor == "" {
				break
			}
			cursor = nextCursor
		}
		encoded, _ := json.Marshal(revisions)
		return fmt.Sprintf("%x", sha256.Sum256(encoded)), pages, nil
	}()
	if err != nil || !includeTools || pages == nil {
		return identity, nil, err
	}
	var tools []mcpschema.Tool
	for _, page := range pages {
		var names []string
		metadataOnly := false
		if selection != nil {
			names = selection.names
			if names == nil {
				names = []string{}
			}
			metadataOnly = selection.metadataOnly
		}
		selected, err := page.Select(names, metadataOnly)
		if err != nil {
			return "", nil, err
		}
		tools = append(tools, selected...)
	}
	return identity, tools, nil
}

// ToolCatalogGeneration exposes invalidation state without catalog contents.
func (m *Manager) ToolCatalogGeneration(server string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.toolCatalogGeneration[server]
}

// MayHaveToolCatalogAlias is only a candidate hint: it cannot authorize or return a
// tool. Only a complete public catalog can establish a negative hint; private
// catalogs remain unknown for another caller. Callers must subsequently select
// the current scoped snapshot. Keeping
// names beside the manager's serialized pages avoids decoding unrelated
// catalogs and resolving credentials for aliases that cannot match. Missing
// reusable pages remain an unknown candidate so local/uncacheable clients keep
// their normal discovery path.
func (m *Manager) MayHaveToolCatalogAlias(server, alias string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	privateObserved, completePublic := false, false
	for _, page := range m.toolCatalog {
		if page.server != server || page.ready != nil || !page.reusable(now) {
			continue
		}
		if !page.public {
			privateObserved = true
		}
		if page.public && page.cursor == "first" {
			// Follow this exact scoped generation in the existing page map;
			// partial public pages cannot prove a negative alias hint.
			current, seen := page, map[string]bool{}
			for current != nil && current.public && current.ready == nil && current.reusable(now) && !seen[current.cursor] {
				seen[current.cursor] = true
				if current.nextCursor == "" {
					completePublic = true
					break
				}
				current = m.toolCatalog[page.catalogKey+current.nextCursor]
			}
		}
		for _, name := range page.names {
			full, colon := server+"/"+name, server+":"+name
			if alias == full || alias == colon || alias == mcpname.Canonical(full) || alias == mcpname.Canonical(colon) {
				return true
			}
		}
	}
	// This API has no caller context. Another user's private catalog can
	// suggest a candidate, but can never exclude an unseen private tool.
	return privateObserved || !completePublic
}

// fetchToolCatalog measures source work separately from cached page decoding.
// It records configured provider names only, never tokens or tool arguments.
func (c *managedClient) fetchToolCatalog(ctx context.Context, client mcpclient.Interface, cursor *string, cache observe.Cache, options ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	span := observe.Start(ctx, "mcp.tools.list.source", observe.Labels{Provider: c.serverName})
	result, err := client.ListTools(ctx, cursor, options...)
	span.End(observe.OutcomeFor(err), cache)
	return result, err
}

func decodeToolCatalogPage(payload []byte) (*mcpschema.ListToolsResult, error) {
	type plain mcpschema.ListToolsResult
	var result plain
	wire := struct {
		*plain
		CacheScope string `json:"cacheScope"`
	}{plain: &result}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, err
	}
	result.CacheScope = mcpschema.ListToolsResultCacheScope(wire.CacheScope)
	return (*mcpschema.ListToolsResult)(&result), nil
}
