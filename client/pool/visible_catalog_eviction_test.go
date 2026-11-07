package manager

import (
	"context"
	"encoding/json"
	"fmt"
	primitive "github.com/viant/mcp/client/catalog/index"
	"testing"
	"time"
)

func TestVisibleCatalogEvictsIdlePartitions(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	load := func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { return nil, nil }
	first, err := m.VisibleCatalog(ctx, "old", time.Time{}, load)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < visibleCatalogCapacity*2; i++ {
		if _, err = m.VisibleCatalog(ctx, fmt.Sprint(i), time.Time{}, load); err != nil {
			t.Fatal(i, err)
		}
	}
	if len(m.visibleCatalogs) != visibleCatalogCapacity {
		t.Fatal(len(m.visibleCatalogs))
	}
	if err = m.CheckVisibleCatalog(ctx, first); err == nil {
		t.Fatal("evicted cursor accepted")
	}
}

func TestVisibleCatalogEntryChurnKeepsRecentlyUsedPartition(t *testing.T) {
	m, _ := New(nil)
	ctx := context.Background()
	p := m.PublicCatalogPartition("window")
	read := func(key string) {
		t.Helper()
		_, err := m.VisibleCatalogEntryInPartition(ctx, key, p, "entry", func(context.Context) (json.RawMessage, time.Time, error) {
			return json.RawMessage(`{}`), time.Time{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	read("hot")
	for i := 0; i < visibleCatalogCapacity*2; i++ {
		read("hot")
		read(fmt.Sprint(i))
	}
	if m.visibleCatalogs["hot"] == nil {
		t.Fatal("recently used partition evicted")
	}
	if len(m.visibleCatalogs) != visibleCatalogCapacity {
		t.Fatal(len(m.visibleCatalogs))
	}
}

func TestVisibleCatalogEvictionProtectsAllPendingLoads(t *testing.T) {
	m, _ := New(nil)
	m.visibleCatalogs = map[string]*visibleCatalog{}
	for i := 0; i < visibleCatalogCapacity; i++ {
		m.visibleCatalogs[fmt.Sprint(i)] = &visibleCatalog{ready: make(chan struct{})}
	}
	m.visibleCatalogs["0"] = &visibleCatalog{entries: []*visibleCatalogEntry{{ready: make(chan struct{})}}}
	m.visibleCatalogs["1"] = &visibleCatalog{entries: []*visibleCatalogEntry{{representations: map[string]*visibleCatalogRepresentation{"json": {ready: make(chan struct{})}}}}}
	if m.makeVisibleCatalogRoomLocked() {
		t.Fatal("evicted pending load")
	}
	m.visibleCatalogs["2"].ready = nil
	if !m.makeVisibleCatalogRoomLocked() {
		t.Fatal("did not evict idle slot")
	}
	if m.visibleCatalogs["0"] == nil || m.visibleCatalogs["1"] == nil || m.visibleCatalogs["2"] != nil {
		t.Fatal("wrong eviction victim")
	}
}
