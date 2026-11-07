// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	primitive "github.com/viant/mcp/client/catalog/index"
	"github.com/viant/mcp/client/observe"
)

// Keyed definitions live in the existing manager catalog slot, sorted within
// one provider/namespace partition. Each population uses the same publication
// and resource generation fences as catalog indexes.
type visibleCatalogEntry struct {
	key             string
	ready           chan struct{}
	invalidated     bool
	value           json.RawMessage
	until           time.Time
	representations map[string]*visibleCatalogRepresentation
}

func (m *Manager) VisibleCatalogEntryInPartition(ctx context.Context, key string, partition VisibleCatalogPartition, entryKey string, load func(context.Context) (json.RawMessage, time.Time, error), aliases ...func(json.RawMessage) []string) (json.RawMessage, error) {
	return m.visibleCatalogEntryInPartition(ctx, key, partition, entryKey, false, load, aliases...)
}

// RefreshVisibleCatalogEntryInPartition replaces one metadata entry using the
// same owner/generation/publication fences. Existing callers retain detached
// bytes and their original deadlines; a refresh never renews a held grant.
func (m *Manager) RefreshVisibleCatalogEntryInPartition(ctx context.Context, key string, partition VisibleCatalogPartition, entryKey string, load func(context.Context) (json.RawMessage, time.Time, error), aliases ...func(json.RawMessage) []string) (json.RawMessage, error) {
	return m.visibleCatalogEntryInPartition(ctx, key, partition, entryKey, true, load, aliases...)
}

func (m *Manager) visibleCatalogEntryInPartition(ctx context.Context, key string, partition VisibleCatalogPartition, entryKey string, refresh bool, load func(context.Context) (json.RawMessage, time.Time, error), aliases ...func(json.RawMessage) []string) (json.RawMessage, error) {
	if ctx == nil || key == "" || entryKey == "" || load == nil || partition.manager != m {
		return nil, primitive.ErrCatalogCursor
	}
	receiver := m.resourceReceiver(ctx)
	if partition.owner == ([32]byte{}) {
		receiver = [32]byte{}
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lookup := observe.Start(ctx, "manager.entry.lookup", observe.Labels{})
		m.mu.Lock()
		if partition.sourceVersions != nil {
			for name, held := range *partition.sourceVersions {
				if m.resourceCatalogGenerationLocked(name, receiver) != held {
					m.mu.Unlock()
					lookup.End(observe.Error, observe.CacheUnknown)
					return nil, primitive.ErrCatalogCursor
				}
			}
		}
		if m.visibleCatalogs == nil {
			m.visibleCatalogs = map[string]*visibleCatalog{}
		}
		slot := m.visibleCatalogs[key]
		slotNew, entryExpired, entryMissing := false, false, false
		if slot == nil {
			slotNew = true
			now := time.Now()
			for name, existing := range m.visibleCatalogs {
				if existing.ready == nil && !existing.until.IsZero() && !existing.until.After(now) {
					delete(m.visibleCatalogs, name)
				}
			}
			if !m.makeVisibleCatalogRoomLocked() {
				m.mu.Unlock()
				lookup.End(observe.Error, observe.CacheUnknown)
				return nil, fmt.Errorf("visible catalog cache capacity exceeded")
			}
			slot = &visibleCatalog{lastUsed: now, partition: partition, receiver: receiver}
			if partition.sources != nil {
				slot.sources = *partition.sources
			}
			m.visibleCatalogs[key] = slot
		} else if !sameCatalogPartition(slot.partition, partition) {
			m.mu.Unlock()
			lookup.End(observe.Error, observe.CacheUnknown)
			return nil, primitive.ErrCatalogCursor
		}
		slot.lastUsed = time.Now()
		index := sort.Search(len(slot.entries), func(i int) bool { return slot.entries[i].key >= entryKey })
		var entry *visibleCatalogEntry
		var replacedValue json.RawMessage
		var replacedAliases []string
		if index < len(slot.entries) && slot.entries[index].key == entryKey {
			entry = slot.entries[index]
			if entry.ready != nil {
				ready := entry.ready
				m.mu.Unlock()
				lookup.End(observe.OK, observe.CacheMiss)
				select {
				case <-ready:
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if !refresh && (entry.until.IsZero() || entry.until.After(time.Now())) {
				out := bytes.Clone(entry.value)
				m.mu.Unlock()
				lookup.End(observe.OK, observe.CacheHit)
				return out, nil
			}
			if refresh {
				replacedValue = bytes.Clone(entry.value)
				for _, candidate := range slot.entries {
					if candidate.key != entryKey && candidate.ready == nil && bytes.Equal(candidate.value, replacedValue) {
						replacedAliases = append(replacedAliases, candidate.key)
					}
				}
			}
			entryExpired = !entry.until.IsZero() && !entry.until.After(time.Now())
			slot.entries = append(slot.entries[:index], slot.entries[index+1:]...)
		}
		if entry == nil {
			entryMissing = true
		}
		entry = &visibleCatalogEntry{key: entryKey, ready: make(chan struct{}), representations: map[string]*visibleCatalogRepresentation{}}
		slot.entries = append(slot.entries, nil)
		copy(slot.entries[index+1:], slot.entries[index:])
		slot.entries[index] = entry
		m.mu.Unlock()
		if slotNew {
			s := observe.Start(ctx, "manager.entry.slot.new", observe.Labels{})
			s.End(observe.OK, observe.CacheMiss)
		}
		if entryExpired {
			s := observe.Start(ctx, "manager.entry.expired", observe.Labels{})
			s.End(observe.OK, observe.CacheMiss)
		}
		if entryMissing {
			s := observe.Start(ctx, "manager.entry.key.miss", observe.Labels{})
			s.End(observe.OK, observe.CacheMiss)
		}
		lookup.End(observe.OK, observe.CacheMiss)
		loadTiming := observe.Start(ctx, "manager.entry.load", observe.Labels{})
		value, until, err := load(ctx)
		loadTiming.End(observe.OutcomeFor(err), observe.CacheMiss)
		if err == nil {
			err = ctx.Err()
		}
		aliasKeys := append([]string(nil), replacedAliases...)
		if err == nil && len(aliases) > 0 && aliases[0] != nil {
			aliasKeys = append(aliasKeys, aliases[0](value)...)
		}
		m.mu.Lock()
		current := m.visibleCatalogs[key]
		if current != slot && err == nil {
			err = primitive.ErrCatalogCursor
		}
		if entry.invalidated && err == nil {
			err = primitive.ErrCatalogCursor
		}
		if err == nil && partition.sourceVersions != nil {
			for name, held := range *partition.sourceVersions {
				if m.resourceCatalogGenerationLocked(name, receiver) != held {
					err = primitive.ErrCatalogCursor
					break
				}
			}
		}
		if err == nil && !until.IsZero() && !until.After(time.Now()) {
			err = primitive.ErrCatalogCursor
		}
		if err == nil {
			entry.value = bytes.Clone(value)
			entry.until = until
		} else if current == slot {
			index = sort.Search(len(slot.entries), func(i int) bool { return slot.entries[i].key >= entryKey })
			if index < len(slot.entries) && slot.entries[index] == entry {
				slot.entries = append(slot.entries[:index], slot.entries[index+1:]...)
			}
		}
		if err == nil {
			for _, alias := range aliasKeys {
				if alias == entryKey || alias == "" {
					continue
				}
				position := sort.Search(len(slot.entries), func(i int) bool { return slot.entries[i].key >= alias })
				if position < len(slot.entries) && slot.entries[position].key == alias {
					prior := slot.entries[position]
					if refresh && prior.ready == nil && bytes.Equal(prior.value, replacedValue) {
						slot.entries[position] = &visibleCatalogEntry{key: alias, value: entry.value, until: entry.until, representations: entry.representations}
					}
					continue
				}
				shared := &visibleCatalogEntry{key: alias, value: entry.value, until: entry.until, representations: entry.representations}
				slot.entries = append(slot.entries, nil)
				copy(slot.entries[position+1:], slot.entries[position:])
				slot.entries[position] = shared
			}
		}
		close(entry.ready)
		entry.ready = nil
		m.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return bytes.Clone(value), nil
	}
}

// PublicCatalogPartition is an explicit trusted metadata classification, not
// evidence that tool execution or user-filtered definitions are public.
func (m *Manager) PublicCatalogPartition(category string) VisibleCatalogPartition {
	return VisibleCatalogPartition{manager: m, category: category}
}
