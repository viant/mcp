package manager

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authctx "github.com/viant/mcp/client/auth/link"
	identity "github.com/viant/mcp/client/catalog"
	primitive "github.com/viant/mcp/client/catalog/index"
	"github.com/viant/mcp/client/observe"
)

type catalogEvents struct {
	mu     sync.Mutex
	events []observe.Event
}

func (r *catalogEvents) Observe(event observe.Event) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}
func (r *catalogEvents) snapshot() []observe.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]observe.Event(nil), r.events...)
}
func requireCatalogEvent(t *testing.T, events []observe.Event, phase string, outcome observe.Outcome, cache observe.Cache) {
	t.Helper()
	for _, event := range events {
		if string(event.Phase) == phase && event.Outcome == outcome && event.Cache == cache {
			return
		}
	}
	t.Fatalf("missing cache stage %s/%s/%s in %+v", phase, outcome, cache, events)
}

func TestVisibleCatalogObserverReportsMissHitAndValidationOutsideLocks(t *testing.T) {
	manager, err := New(nil)
	require.NoError(t, err)
	var recorder catalogEvents
	ctx := authctx.WithCanonicalUserID(authctx.WithBearer(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "private-sensitive-user"}), "private-sensitive-token"), "private-sensitive-owner")
	ctx = observe.WithObserver(ctx, observe.ObserverFunc(func(event observe.Event) {
		// Single-threaded proof: a callback emitted under the cache mutex would fail
		// immediately rather than deadlock the test through observer re-entry.
		if !manager.mu.TryLock() {
			t.Error("observer called under manager cache mutex")
		} else {
			manager.mu.Unlock()
		}
		recorder.Observe(event)
	}))
	partition := manager.CatalogPartition(ctx, "verified-private-scope", "report")
	load := func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		return []primitive.CatalogItem[json.RawMessage]{{URI: "report://users/private-sensitive-resource", Name: "private-sensitive-name", Value: json.RawMessage(`{"secret":"private-sensitive-payload"}`)}}, nil
	}
	first, err := manager.VisibleCatalogInPartition(ctx, "private-sensitive-key", partition, time.Now().Add(time.Minute), load)
	require.NoError(t, err)
	second, err := manager.VisibleCatalogInPartition(ctx, "private-sensitive-key", partition, time.Now().Add(time.Minute), load)
	require.NoError(t, err)
	require.Same(t, first, second)
	require.NoError(t, manager.CheckVisibleCatalog(ctx, first))
	_, _, err = manager.CatalogScope(ctx, "private-sensitive-conversation", nil)
	require.NoError(t, err)
	manager.InvalidateVisibleCatalogPartition(partition)
	require.ErrorIs(t, manager.CheckVisibleCatalog(ctx, first), primitive.ErrCatalogCursor)
	events := recorder.snapshot()
	requireCatalogEvent(t, events, "manager.cache.lookup", observe.OK, observe.CacheMiss)
	requireCatalogEvent(t, events, "manager.cache.lookup", observe.OK, observe.CacheHit)
	requireCatalogEvent(t, events, "manager.cache.populate", observe.OK, observe.CacheMiss)
	requireCatalogEvent(t, events, "manager.cache.publish", observe.OK, observe.CacheMiss)
	requireCatalogEvent(t, events, "manager.cache.validate", observe.OK, observe.CacheHit)
	requireCatalogEvent(t, events, "manager.cache.validate", observe.Error, observe.CacheMiss)
	requireCatalogEvent(t, events, "manager.catalog.scope", observe.OK, observe.CacheUnknown)
	raw, err := json.Marshal(events)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "private-sensitive"), "timings must not carry keys, owners, tokens, URIs or payloads")
	for _, event := range events {
		require.Equal(t, observe.Labels{}, event.Labels)
		require.GreaterOrEqual(t, event.Duration, time.Duration(0))
	}
}

func TestVisibleCatalogObserverReportsInflightWaitAndCancellation(t *testing.T) {
	for _, cancelFollower := range []bool{false, true} {
		t.Run(map[bool]string{false: "ready", true: "canceled"}[cancelFollower], func(t *testing.T) {
			manager, err := New(nil)
			require.NoError(t, err)
			entered, release := make(chan struct{}), make(chan struct{})
			leaderDone := make(chan error, 1)
			go func() {
				_, err := manager.VisibleCatalog(context.Background(), "shared", time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
					close(entered)
					<-release
					return nil, nil
				})
				leaderDone <- err
			}()
			<-entered
			var recorder catalogEvents
			followerEntered := make(chan struct{})
			var signal sync.Once
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = observe.WithObserver(ctx, observe.ObserverFunc(func(event observe.Event) {
				// Real re-entry also verifies that the wait/lookup observer is outside locks.
				manager.ToolCatalogGeneration("configured-server")
				recorder.Observe(event)
				if string(event.Phase) == "manager.cache.lookup" && event.Cache == observe.CacheMiss {
					signal.Do(func() { close(followerEntered) })
				}
			}))
			followerDone := make(chan error, 1)
			go func() {
				_, err := manager.VisibleCatalog(ctx, "shared", time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
					t.Error("follower repopulated an active source")
					return nil, nil
				})
				followerDone <- err
			}()
			<-followerEntered
			if cancelFollower {
				cancel()
				require.ErrorIs(t, <-followerDone, context.Canceled)
				close(release)
			} else {
				close(release)
				require.NoError(t, <-followerDone)
			}
			require.NoError(t, <-leaderDone)
			events := recorder.snapshot()
			if cancelFollower {
				requireCatalogEvent(t, events, "manager.cache.wait", observe.Canceled, observe.CacheUnknown)
			} else {
				requireCatalogEvent(t, events, "manager.cache.wait", observe.OK, observe.CacheUnknown)
				requireCatalogEvent(t, events, "manager.cache.lookup", observe.OK, observe.CacheHit)
			}
		})
	}
}

func TestVisibleCatalogObserverReportsDeniedLoadAndFencedPublication(t *testing.T) {
	manager, err := New(nil)
	require.NoError(t, err)
	var recorder catalogEvents
	ctx := observe.WithObserver(context.Background(), &recorder)
	_, err = manager.VisibleCatalog(ctx, "denied", time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		return nil, identity.ErrResourceDenied
	})
	require.ErrorIs(t, err, identity.ErrResourceDenied)
	requireCatalogEvent(t, recorder.snapshot(), "manager.cache.populate", observe.Denied, observe.CacheMiss)
	requireCatalogEvent(t, recorder.snapshot(), "manager.cache.publish", observe.Denied, observe.CacheMiss)
	partition := manager.CatalogPartition(ctx, "stable-verified-owner", "report")
	_, err = manager.VisibleCatalogInPartition(ctx, "fenced", partition, time.Now().Add(time.Minute), func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) {
		manager.InvalidateVisibleCatalogPartition(partition)
		return nil, nil
	})
	require.ErrorIs(t, err, primitive.ErrCatalogCursor)
	requireCatalogEvent(t, recorder.snapshot(), "manager.cache.publish", observe.Error, observe.CacheMiss)
}

func BenchmarkVisibleCatalogWarmObservedLookup(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		b.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(b *testing.B) {
			manager, err := New(nil)
			if err != nil {
				b.Fatal(err)
			}
			ctx := context.Background()
			if enabled {
				ctx = observe.WithObserver(ctx, observe.ObserverFunc(func(observe.Event) {}))
			}
			until := time.Now().Add(time.Hour)
			load := func(context.Context) ([]primitive.CatalogItem[json.RawMessage], error) { return nil, nil }
			if _, err := manager.VisibleCatalog(ctx, "warm", until, load); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := manager.VisibleCatalog(ctx, "warm", until, load); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
