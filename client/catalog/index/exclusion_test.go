package primitive

import (
	"context"
	"reflect"
	"testing"
)

func TestCatalogMultipleKeywordsAndExclusion(t *testing.T) {
	catalog := NewCatalog("one", []CatalogItem[string]{
		{Name: "Alpha review", Description: "Current summary", Namespace: "one", Value: "alpha"},
		{Name: "Beta review", Description: "Archived summary", Namespace: "one", Value: "beta"},
		{Name: "Gamma preview", Description: "Current summary", Namespace: "one", Value: "gamma"},
		{Name: "Delta", Description: "Current review", Namespace: "one", Value: "delta"},
		{Name: "Literal .*", Description: "Current review", Namespace: "one", Value: "literal"},
		{Name: "Alpha outside", Namespace: "two", Value: "outside"},
	})
	query := CatalogQuery{Namespace: "one", Keywords: []string{"ALPHA", "review"}, ExcludeKeywords: []string{" archived ", "PREVIEW", ".*"}, Limit: 1}
	page, err := catalog.Page(context.Background(), query)
	if err != nil || page.Total != 2 || !reflect.DeepEqual(page.Items, []string{"alpha"}) || page.Complete {
		t.Fatalf("first page: %+v, %v", page, err)
	}
	query.Cursor = page.NextCursor
	// Case, whitespace, duplicates and ordering retain the same query identity.
	query.ExcludeKeywords = []string{".*", "preview", "archived", "ARCHIVED", " "}
	page, err = catalog.Page(context.Background(), query)
	if err != nil || !page.Complete || !reflect.DeepEqual(page.Items, []string{"delta"}) {
		t.Fatalf("second page: %+v, %v", page, err)
	}
	query.ExcludeKeywords = []string{"alpha"}
	if _, err = catalog.Page(context.Background(), query); err != ErrCatalogCursor {
		t.Fatalf("changed exclusion must invalidate cursor: %v", err)
	}
	query.Cursor = ""
	query.Keywords = []string{"alpha"}
	page, err = catalog.Page(context.Background(), query)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("exclusion must win over inclusion: %+v, %v", page, err)
	}
}
