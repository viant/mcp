// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core; provider bindings are independent of agent execution.
package primitive

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestCatalogLocalQueriesUseOneSortedNamespaceIndex(t *testing.T) {
	rows := []CatalogItem[int]{
		{Name: "Zulu", Description: "Sales dashboard", Namespace: "a", URI: "window://a/z", Provider: "p", Value: 1},
		{Name: "alpha", Description: "Sales details", Namespace: "a", URI: "window://a/a", Provider: "p", Value: 2},
		{Name: "alpha", Description: "Inventory", Namespace: "b", URI: "window://b/a", Provider: "q", Value: 3},
		{Name: "Beta", Description: "Sales forecast", Namespace: "b", URI: "window://b/b", Provider: "q", Value: 4},
	}
	before := append([]CatalogItem[int](nil), rows...)
	index := NewCatalog("snapshot", rows)
	if len(index.items) != 4 || len(index.order) != 4 || len(index.partitions["a"])+len(index.partitions["b"]) != 4 {
		t.Fatal("unexpected duplicated storage")
	}
	if &index.items[0] != &rows[0] {
		t.Fatal("entries copied instead of retained once")
	}
	order := append([]uint32(nil), index.order...)
	partition := &index.partitions["a"][0]
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			page, err := index.Page(context.Background(), CatalogQuery{Namespace: "a", NameTerms: []string{"ALP", "does not match"}, DescriptionTerms: []string{"sales"}, Keywords: []string{"details"}, Limit: 1})
			if err != nil || !reflect.DeepEqual(page.Items, []int{2}) {
				t.Errorf("local grouped filter: %v %v", page, err)
			}
		}()
	}
	wg.Wait()
	if !reflect.DeepEqual(index.order, order) || &index.partitions["a"][0] != partition || !reflect.DeepEqual(rows, before) {
		t.Fatal("queries modified/rebuilt stored catalog or indexes")
	}
	query := CatalogQuery{Limit: 1}
	first, err := index.Page(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Items, []int{2}) {
		t.Fatal(first.Items)
	}
	query.Cursor = first.NextCursor
	second, err := index.Page(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.Items, []int{3}) {
		t.Fatal(second.Items)
	}
	duplicate, err := index.Page(context.Background(), query)
	if err != nil || duplicate.NextCursor != second.NextCursor {
		t.Fatal("non-deterministic cursor")
	}
	query.Namespace = "a"
	if _, err = index.Page(context.Background(), query); err != ErrCatalogCursor {
		t.Fatalf("query mismatch: %v", err)
	}
	query.Namespace = ""
	if _, err = NewCatalog("new-snapshot", rows).Page(context.Background(), query); err != ErrCatalogCursor {
		t.Fatalf("snapshot mismatch: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = index.Page(canceled, CatalogQuery{}); err != context.Canceled {
		t.Fatal(err)
	}
	literal, err := index.Page(context.Background(), CatalogQuery{Keywords: []string{".*"}})
	if err != nil || literal.Total != 0 {
		t.Fatal("terms interpreted as patterns")
	}
}

func TestCatalogOwnerFilterIsExactAndBindsCursor(t *testing.T) {
	index := NewCatalog("owner-filter", []CatalogItem[int]{
		{Name: "a", OwnerID: "Alice", Namespace: "users", Value: 1},
		{Name: "b", OwnerID: "alice", Namespace: "users", Value: 2},
		{Name: "c", OwnerID: "Alice", Namespace: "users", Value: 3},
		{Name: "d", Namespace: "public", Value: 4},
	})
	query := CatalogQuery{OwnerIDs: []string{"Alice"}, Limit: 1}
	first, err := index.Page(context.Background(), query)
	if err != nil || !reflect.DeepEqual(first.Items, []int{1}) || first.Total != 2 {
		t.Fatalf("owner matching changed case or included unowned rows: %+v %v", first, err)
	}
	query.Cursor = first.NextCursor
	second, err := index.Page(context.Background(), query)
	if err != nil || !reflect.DeepEqual(second.Items, []int{3}) || !second.Complete {
		t.Fatalf("owner page: %+v %v", second, err)
	}
	query.OwnerIDs = []string{"alice"}
	if _, err := index.Page(context.Background(), query); err != ErrCatalogCursor {
		t.Fatalf("owner change reused prior cursor: %v", err)
	}
}
