// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	primitive "github.com/viant/mcp/client/catalog/index"
	"github.com/viant/mcp/client/observe"
	requestctx "github.com/viant/mcp/client/requestctx"
)

// VisibleCatalogPartition tags snapshots by stable authenticated owner and
// resource category. It is cache infrastructure, never a protocol grant.
type VisibleCatalogPartition struct {
	manager        *Manager
	owner          [32]byte
	category       string
	sources        *map[string]bool
	sourceVersions *map[string][2]uint64
}

// CatalogPartition excludes connection generations, credentials and conversation
// IDs so writes invalidate the same owner's retained credential/session variants.
// verifiedScope supplies the gateway's stable verified issuer/subject/tenant.
// A canonical workspace user unifies its verified session/provider variants.
func (m *Manager) CatalogPartition(ctx context.Context, verifiedScope, category string) VisibleCatalogPartition {
	if m == nil || ctx == nil || verifiedScope == "" || category == "" {
		return VisibleCatalogPartition{}
	}
	user := authctx.EffectiveUserID(ctx)
	if m.userIDFn != nil {
		user = m.userIDFn(ctx)
	}
	principal := []string{user, authctx.EffectiveUserID(ctx), authctx.Provider(ctx), verifiedScope}
	if canonical := authctx.CanonicalUserID(ctx); canonical != "" {
		principal = []string{"canonical", canonical}
	}
	raw, _ := json.Marshal(principal)
	return VisibleCatalogPartition{manager: m, owner: sha256.Sum256(raw), category: category}
}

// InvalidateVisibleCatalogPartition removes only matching completed or pending
// slots. Existing publication and CheckVisibleCatalog fences reject every
// removed slot, including late populations and cursors held by an earlier read.
func (m *Manager) InvalidateVisibleCatalogPartition(partition VisibleCatalogPartition) {
	if partition.manager != m || partition.category == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, held := range m.visibleCatalogs {
		if sameCatalogPartition(held.partition, partition) {
			delete(m.visibleCatalogs, key)
		}
	}
}

type visibleCatalog struct {
	lastUsed    time.Time
	sources     map[string]bool
	receiver    [32]byte
	partition   VisibleCatalogPartition
	ready       chan struct{}
	index       *primitive.Catalog[json.RawMessage]
	until       time.Time
	sourceUntil time.Time
	entries     []*visibleCatalogEntry
}

// CatalogScope keeps visible metadata private, regardless of tools/list's
// visibility. It reuses current configured/authenticated connection identity.
func (m *Manager) CatalogScope(ctx context.Context, conversation string, names []string) (string, bool, error) {
	return m.CatalogScopeForResources(ctx, conversation, names, names)
}

// CatalogScopeForResources retains all transport/schema fences while resource
// events affect only the catalog's actual contributing servers.
func (m *Manager) CatalogScopeForResources(ctx context.Context, conversation string, names []string, resourceNames []string) (key string, cacheable bool, failure error) {
	timing := observe.Start(ctx, observe.Phase("manager.catalog.scope"), observe.Labels{})
	defer func() {
		cache := observe.CacheUnknown
		if failure == nil && !cacheable {
			cache = observe.CacheBypass
		}
		timing.End(observe.OutcomeFor(failure), cache)
	}()
	scope := []string{m.poolKey(ctx, conversation), authctx.EffectiveUserID(ctx), authctx.CanonicalUserID(ctx), requestctx.ConversationIDFromContext(ctx), authctx.Provider(ctx), authctx.Bearer(ctx), authctx.IDToken(ctx), authctx.MCPAuthToken(ctx, false), authctx.MCPAuthToken(ctx, true)}
	names = append([]string(nil), names...)
	sort.Strings(names)
	cacheable = true
	for _, name := range names {
		cfg, err := m.Options(ctx, name)
		if err != nil {
			return "", false, err
		}
		effective := m.WithAuthTokenContext(ctx, name)
		cli, err := m.Get(effective, conversation, name)
		if err != nil {
			return "", false, err
		}
		// Client initialization normalizes shared options (for example missing
		// Name/Version). Capture the effective configuration after that normal
		// initialization, before the catalog load and its final drift check.
		raw, err := json.Marshal(cfg)
		if err != nil {
			return "", false, err
		}
		transportScope, transportCacheable, err := m.catalogTransportCredentialScope(effective, name, cfg)
		if err != nil {
			return "", false, err
		}
		cacheable = cacheable && transportCacheable
		scope = append(scope, transportScope)
		transportToken, _ := effective.Value(authtransport.ContextAuthTokenKey).(string)
		credential := ""
		if cfg != nil && cfg.ClientOptions != nil {
			if cfg.IsDelegatedAuth() {
				if m.credResolver == nil {
					return "", false, fmt.Errorf("catalog credential resolver unavailable")
				}
				requirement, err := cfg.Auth.CompileRequirement(effective, name, cfg.Transport.URL)
				if err != nil {
					return "", false, err
				}
				resolved, err := m.credResolver.Resolve(effective, *requirement)
				if err != nil {
					return "", false, err
				}
				if err = resolved.Verify(requirement, time.Now()); err != nil {
					return "", false, err
				}
				credential = resolved.Token
			}
		}
		m.mu.Lock()
		generation := m.toolCatalogGeneration[name]
		m.mu.Unlock()
		scope = append(scope, fmt.Sprintf("%s:%s:%p:%d:%s:%s", name, raw, cli, generation, transportToken, credential))
	}
	dependencies := append([]string(nil), resourceNames...)
	sort.Strings(dependencies)
	for _, name := range dependencies {
		scope = append(scope, fmt.Sprintf("resource:%s:%v", name, m.ResourceCatalogGeneration(ctx, name)))
	}
	raw, _ := json.Marshal(scope)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum), cacheable, nil
}

// VisibleCatalog is the one manager-owned snapshot store used by primitive
// and native/ordinary skill adapters. A snapshot is metadata, never authority.
func (m *Manager) VisibleCatalog(ctx context.Context, key string, until time.Time, load func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error), sourceLease ...func() time.Time) (*primitive.Catalog[json.RawMessage], error) {
	names, err := m.Names(ctx)
	if err != nil {
		return nil, err
	}
	partition := VisibleCatalogPartition{}
	if len(names) > 0 {
		partition = m.WithCatalogSources(ctx, partition, names)
	}
	return m.VisibleCatalogInPartition(ctx, key, partition, until, load, sourceLease...)
}

// VisibleCatalogInPartition uses the same snapshot store and publication fences
// as VisibleCatalog, attaching an explicit owner/category for scoped mutation.
func (m *Manager) VisibleCatalogInPartition(ctx context.Context, key string, partition VisibleCatalogPartition, until time.Time, load func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error), sourceLease ...func() time.Time) (*primitive.Catalog[json.RawMessage], error) {
	if partition.manager != nil && partition.manager != m {
		return nil, primitive.ErrCatalogCursor
	}

	receiver := m.resourceReceiver(ctx)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lookup := observe.Start(ctx, observe.Phase("manager.cache.lookup"), observe.Labels{})
		m.mu.Lock()
		if partition.sourceVersions != nil {
			for name, held := range *partition.sourceVersions {
				if m.resourceCatalogGenerationLocked(name, receiver) != held {
					m.mu.Unlock()
					lookup.End(observe.Error, observe.CacheMiss)
					return nil, primitive.ErrCatalogCursor
				}
			}
		}
		if m.visibleCatalogs == nil {
			m.visibleCatalogs = map[string]*visibleCatalog{}
		}
		now := time.Now()
		if held := m.visibleCatalogs[key]; held != nil {
			held.lastUsed = now
			if held.ready != nil {
				ready := held.ready
				m.mu.Unlock()
				lookup.End(observe.OK, observe.CacheMiss)
				wait := observe.Start(ctx, observe.Phase("manager.cache.wait"), observe.Labels{})
				select {
				case <-ready:
					wait.End(observe.OK, observe.CacheUnknown)
					continue
				case <-ctx.Done():
					wait.End(observe.Canceled, observe.CacheUnknown)
					return nil, ctx.Err()
				}
			}
			if sameCatalogPartition(held.partition, partition) && (held.until.IsZero() || held.until.After(now)) {
				index := held.index
				m.mu.Unlock()
				lookup.End(observe.OK, observe.CacheHit)
				return index, nil
			}
			delete(m.visibleCatalogs, key)
		}
		for k, v := range m.visibleCatalogs {
			if v.ready == nil && !v.until.IsZero() && !v.until.After(now) {
				delete(m.visibleCatalogs, k)
			}
		}
		if !m.makeVisibleCatalogRoomLocked() {
			m.mu.Unlock()
			lookup.End(observe.Error, observe.CacheBypass)
			return nil, fmt.Errorf("visible catalog cache capacity exceeded")
		}
		if !until.After(now) {
			until = time.Time{}
		}
		pending := &visibleCatalog{lastUsed: now, partition: partition, ready: make(chan struct{}), receiver: receiver}
		if partition.sources != nil {
			pending.sources = *partition.sources
		}
		m.visibleCatalogs[key] = pending
		if m.catalogSequence == 0 {
			if _, err := rand.Read(m.catalogEpoch[:]); err != nil {
				delete(m.visibleCatalogs, key)
				m.mu.Unlock()
				lookup.End(observe.OutcomeFor(err), observe.CacheMiss)
				return nil, err
			}
		}
		m.catalogSequence++
		snapshot := fmt.Sprintf("%s:%x:%d", key, m.catalogEpoch, m.catalogSequence)
		m.mu.Unlock()
		lookup.End(observe.OK, observe.CacheMiss)
		populate := observe.Start(ctx, observe.Phase("manager.cache.populate"), observe.Labels{})
		items, err := load(ctx)
		var sourceUntil time.Time
		if err == nil && len(sourceLease) > 0 && sourceLease[0] != nil {
			sourceUntil = sourceLease[0]()
			if !sourceUntil.IsZero() {
				if !sourceUntil.After(time.Now()) {
					err = primitive.ErrCatalogCursor
				}
				if !until.IsZero() && sourceUntil.Before(until) {
					until = sourceUntil
				}
			}
		}
		var index *primitive.Catalog[json.RawMessage]
		if err == nil && ctx.Err() == nil {
			index = primitive.NewCatalog(snapshot, items)
		}
		outcomeErr := err
		if outcomeErr == nil {
			outcomeErr = ctx.Err()
		}
		populate.End(observe.OutcomeFor(outcomeErr), observe.CacheMiss)
		publish := observe.Start(ctx, observe.Phase("manager.cache.publish"), observe.Labels{})
		m.mu.Lock()
		if m.visibleCatalogs[key] != pending && err == nil {
			err = primitive.ErrCatalogCursor
		}
		if m.visibleCatalogs[key] == pending {
			if err == nil && ctx.Err() == nil {
				pending.index = index
				pending.until = until
				pending.sourceUntil = sourceUntil
			} else {
				delete(m.visibleCatalogs, key)
			}
		}
		close(pending.ready)
		pending.ready = nil
		m.mu.Unlock()
		outcomeErr = err
		if outcomeErr == nil {
			outcomeErr = ctx.Err()
		}
		publish.End(observe.OutcomeFor(outcomeErr), observe.CacheMiss)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return index, nil
	}
}

// CheckVisibleCatalog linearizes delivery against invalidation and eviction.
func (m *Manager) CheckVisibleCatalog(ctx context.Context, index *primitive.Catalog[json.RawMessage]) error {
	timing := observe.Start(ctx, observe.Phase("manager.cache.validate"), observe.Labels{})
	if err := ctx.Err(); err != nil {
		timing.End(observe.OutcomeFor(err), observe.CacheBypass)
		return err
	}
	m.mu.Lock()
	for _, held := range m.visibleCatalogs {
		if held.ready == nil && held.index == index && (held.until.IsZero() || held.until.After(time.Now())) && (held.sourceUntil.IsZero() || held.sourceUntil.After(time.Now())) {
			m.mu.Unlock()
			timing.End(observe.OK, observe.CacheHit)
			return nil
		}
	}
	m.mu.Unlock()
	timing.End(observe.Error, observe.CacheMiss)
	return primitive.ErrCatalogCursor
}
