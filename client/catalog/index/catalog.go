// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core; provider bindings are independent of agent execution.
package primitive

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/viant/embedius/bm25"
	"sort"
	"strconv"
	"strings"
)

// CatalogQuery applies literal, case-insensitive contains matching locally.
// Terms are OR within a group; supplied groups and Namespace are AND.
const MaxCatalogPageSize = 1000

type CatalogQuery struct {
	// Search ranks natural-language terms with BM25. Literal filters still apply.
	Search           string   `json:"search,omitempty"`
	OwnerIDs         []string `json:"ownerIds,omitempty"`
	Namespace        string   `json:"namespace,omitempty"`
	NameTerms        []string `json:"nameTerms,omitempty"`
	DescriptionTerms []string `json:"descriptionTerms,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	// ExcludeKeywords rejects any literal name or description match; exclusion wins.
	ExcludeKeywords []string `json:"excludeKeywords,omitempty"`
	Limit           int      `json:"limit,omitempty"`
	Cursor          string   `json:"cursor,omitempty"`
}
type CatalogPage[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
	Complete   bool   `json:"complete"`
	Total      int    `json:"total"`
}
type CatalogItem[T any] struct {
	Name, Description, Namespace, URI, Provider, OwnerID string
	Value                                                T
}
type catalogText struct{ name, description string }

// Catalog owns its input entries once. Only compact indexes are duplicated for
// namespace partitions. The immutable index supports concurrent queries; no
// query sorts or copies the full catalog. Values must remain immutable.
type Catalog[T any] struct {
	items      []CatalogItem[T]
	text       []catalogText
	order      []uint32
	partitions map[string][]uint32
	id         string
	lexical    *bm25.Index
	lexicalErr error
}

var ErrCatalogCursor = errors.New("catalog cursor does not match current snapshot and query")

func NewCatalog[T any](snapshot string, items []CatalogItem[T]) *Catalog[T] {
	c := &Catalog[T]{items: items, text: make([]catalogText, len(items)), order: make([]uint32, len(items)), partitions: map[string][]uint32{}}
	for i, item := range items {
		c.text[i] = catalogText{strings.ToLower(item.Name), strings.ToLower(item.Description)}
		c.order[i] = uint32(i)
	}
	sort.Slice(c.order, func(i, j int) bool {
		a, b := c.order[i], c.order[j]
		if c.text[a].name != c.text[b].name {
			return c.text[a].name < c.text[b].name
		}
		if c.items[a].Name != c.items[b].Name {
			return c.items[a].Name < c.items[b].Name
		}
		if c.items[a].URI != c.items[b].URI {
			return c.items[a].URI < c.items[b].URI
		}
		return c.items[a].Provider < c.items[b].Provider
	})
	for _, i := range c.order {
		ns := items[i].Namespace
		c.partitions[ns] = append(c.partitions[ns], i)
	}
	documents := make([]bm25.Document, 0, len(c.order))
	for _, i := range c.order {
		item := c.items[i]
		documents = append(documents, bm25.Document{ID: strconv.FormatUint(uint64(i), 10), Text: strings.Join([]string{item.Name, item.Description, item.Namespace, item.URI, item.Provider}, " ")})
	}
	// Ordinal IDs are unique and construction has no external dependencies.
	c.lexical, c.lexicalErr = bm25.New(context.Background(), documents)
	sum := sha256.Sum256([]byte(snapshot))
	c.id = base64.RawURLEncoding.EncodeToString(sum[:])
	return c
}
func normalizeTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	seen := map[string]bool{}
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if term != "" && !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
	}
	sort.Strings(out)
	return out
}

// Owner filters are exact identity matches; unlike search terms they are never
// case-folded or interpreted as access grants.
func normalizeOwners(owners []string) []string {
	out := append([]string(nil), owners...)
	sort.Strings(out)
	return out
}
func matchesOwner(owner string, owners []string) bool {
	if len(owners) == 0 {
		return true
	}
	for _, candidate := range owners {
		if owner != "" && owner == candidate {
			return true
		}
	}
	return false
}
func containsAny(value string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	for _, term := range terms {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}
func (c *Catalog[T]) Page(ctx context.Context, query CatalogQuery) (*CatalogPage[T], error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query.Search = strings.TrimSpace(query.Search)
	query.OwnerIDs = normalizeOwners(query.OwnerIDs)
	query.NameTerms = normalizeTerms(query.NameTerms)
	query.DescriptionTerms = normalizeTerms(query.DescriptionTerms)
	query.Keywords = normalizeTerms(query.Keywords)
	query.ExcludeKeywords = normalizeTerms(query.ExcludeKeywords)
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > MaxCatalogPageSize {
		query.Limit = MaxCatalogPageSize
	}
	cursor := query.Cursor
	query.Cursor = ""
	raw, _ := json.Marshal(query)
	digest := sha256.Sum256(raw)
	queryID := base64.RawURLEncoding.EncodeToString(digest[:])
	type position struct {
		Snapshot string `json:"s"`
		Query    string `json:"q"`
		Offset   int    `json:"o"`
	}
	offset := 0
	if cursor != "" {
		bytes, err := base64.RawURLEncoding.DecodeString(cursor)
		var p position
		if err != nil || json.Unmarshal(bytes, &p) != nil || p.Snapshot != c.id || p.Query != queryID || p.Offset < 0 {
			return nil, ErrCatalogCursor
		}
		offset = p.Offset
	}
	order := c.order
	if query.Namespace != "" {
		order = c.partitions[query.Namespace]
	}
	if query.Search != "" {
		if c.lexicalErr != nil {
			return nil, c.lexicalErr
		}
		hits, err := c.lexical.Search(ctx, query.Search, 0)
		if err != nil {
			return nil, err
		}
		order = make([]uint32, 0, len(hits))
		for _, hit := range hits {
			id, err := strconv.ParseUint(hit.ID, 10, 32)
			if err != nil {
				return nil, err
			}
			order = append(order, uint32(id))
		}
	}
	result := &CatalogPage[T]{Items: make([]T, 0, query.Limit)}
	for n, i := range order {
		if n%64 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if query.Namespace != "" && c.items[i].Namespace != query.Namespace {
			continue
		}
		if !matchesOwner(c.items[i].OwnerID, query.OwnerIDs) {
			continue
		}
		text := c.text[i]
		if len(query.ExcludeKeywords) > 0 && (containsAny(text.name, query.ExcludeKeywords) || containsAny(text.description, query.ExcludeKeywords)) {
			continue
		}
		if !containsAny(text.name, query.NameTerms) || !containsAny(text.description, query.DescriptionTerms) {
			continue
		}
		if len(query.Keywords) > 0 && !containsAny(text.name, query.Keywords) && !containsAny(text.description, query.Keywords) {
			continue
		}
		if result.Total >= offset && len(result.Items) < query.Limit {
			result.Items = append(result.Items, c.items[i].Value)
		}
		result.Total++
	}
	if offset > result.Total {
		return nil, ErrCatalogCursor
	}
	result.Complete = offset+len(result.Items) >= result.Total
	if !result.Complete {
		raw, _ := json.Marshal(position{c.id, queryID, offset + len(result.Items)})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
