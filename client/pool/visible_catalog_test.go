package manager

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpclient "github.com/viant/mcp/client"
	authcfg "github.com/viant/mcp/client/auth/config"
	primitive "github.com/viant/mcp/client/catalog/index"
)

func TestVisibleCatalogOwnerCoalescesLoadsExpiresCursorsAndCancels(t *testing.T) {
	owner, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var loads atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	load := func(ctx context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		if loads.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []primitive.CatalogItem[json.RawMessage]{{Name: "alpha", Namespace: "a", URI: "intent://a/alpha", Value: json.RawMessage(`{"name":"alpha"}`)}, {Name: "beta", Namespace: "b", URI: "intent://b/beta", Value: json.RawMessage(`{"name":"beta"}`)}}, nil
	}
	const workers = 12
	indexes := make([]*primitive.Catalog[json.RawMessage], workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			indexes[i], err = owner.VisibleCatalog(context.Background(), "private", time.Now().Add(time.Minute), load)
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	<-started
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = owner.VisibleCatalog(canceled, "private", time.Now().Add(time.Minute), load); err != context.Canceled {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatal("catalog loaded more than once")
	}
	for _, index := range indexes {
		if index != indexes[0] {
			t.Fatal("per-query catalog copy")
		}
	}
	first, err := indexes[0].Page(context.Background(), primitive.CatalogQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	owner.visibleCatalogs["private"].until = time.Now().Add(-time.Second)
	owner.mu.Unlock()
	fresh, err := owner.VisibleCatalog(context.Background(), "private", time.Now().Add(time.Minute), load)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Page(context.Background(), primitive.CatalogQuery{Limit: 1, Cursor: first.NextCursor}); err != primitive.ErrCatalogCursor {
		t.Fatal("expired cursor reused", err)
	}
	another, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := another.VisibleCatalog(context.Background(), "private", time.Now().Add(time.Minute), load)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Page(context.Background(), primitive.CatalogQuery{Limit: 1, Cursor: first.NextCursor}); err != primitive.ErrCatalogCursor {
		t.Fatal("cursor reused after manager restart", err)
	}
	canceledLoad, cancelLoad := context.WithCancel(context.Background())
	cancelLoad()
	if _, err = owner.VisibleCatalog(canceledLoad, "new", time.Now().Add(time.Minute), load); err != context.Canceled {
		t.Fatal(err)
	}
	owner.mu.Lock()
	_, published := owner.visibleCatalogs["new"]
	owner.mu.Unlock()
	if published {
		t.Fatal("canceled metadata published")
	}
}

type nilVisibleCredentialResolver struct{ staticCredentialResolver }

func (nilVisibleCredentialResolver) Resolve(context.Context, authcfg.Requirement) (*authcfg.Credential, error) {
	return nil, nil
}
func TestCatalogScopeNilResolvedCredentialFailsWithoutPublishingMetadata(t *testing.T) {
	config := delegatedConfig(false)
	owner, err := New(&authTokenProviderConfigStub{cfg: config}, WithCredentialResolver(nilVisibleCredentialResolver{}), WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return &stubClient{}, nil }))
	if err != nil {
		t.Fatal(err)
	}
	scope, cacheable, err := owner.CatalogScope(context.Background(), "session", []string{"server"})
	if err == nil || err.Error() != "credential has no token" || scope != "" || cacheable {
		t.Fatalf("nil credential was accepted: %q %v %v", scope, cacheable, err)
	}
	if len(owner.visibleCatalogs) != 0 {
		t.Fatal("nil-credential metadata published")
	}
}
