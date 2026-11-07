package manager

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	"github.com/viant/mcp/client/observe"
)

func noRetrySnapshotFixture(t *testing.T) (*Manager, *catalogProvider, *catalogClient, *managedClient, context.Context) {
	t.Helper()
	m, provider, source, managed, _ := catalogSetup(t, false)
	bff := true
	provider.cfg.Auth = &mcp.ClientAuth{BackendForFrontend: &bff}
	rt, err := authtransport.New()
	require.NoError(t, err)
	m.authRT = rt
	source.zeroTTL = true
	ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	return m, provider, source, managed, ctx
}

func TestNoRetrySnapshotCoalescesEightCallsButNeverCrossesReads(t *testing.T) {
	m, provider, source, managed, base := noRetrySnapshotFixture(t)
	read := WithToolCatalogReadSnapshot(base)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
			if err != nil || result == nil || len(result.Tools) != 1 {
				t.Errorf("shared no-retry tool read: result=%v error=%v", result, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	require.EqualValues(t, 1, source.calls.Load())
	_, err := managed.ListTools(WithToolCatalogReadSnapshot(base), nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 2, source.calls.Load(), "zero TTL remains fresh on the next request")
	_, err = managed.ListTools(base, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 3, source.calls.Load(), "ordinary NoRetry still bypasses every cache")
	_, err = managed.ListTools(authctx.WithBearer(read, "another-token"), nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 4, source.calls.Load(), "credential changes partition one read snapshot")
	provider.cfg.Transport.URL = "https://other.test/mcp"
	_, err = managed.ListTools(read, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 5, source.calls.Load(), "endpoint/configuration changes partition the snapshot")
	otherEntry := &managedClient{mgr: m, serverName: managed.serverName, entry: &entry{client: source}}
	_, err = otherEntry.ListTools(read, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 6, source.calls.Load(), "a new managed connection identity cannot inherit a prior revision")
	(&changeHandler{manager: m, server: "server"}).OnNotification(read, &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	_, err = managed.ListTools(read, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 7, source.calls.Load(), "producer notification rotates the snapshot key")
	cursor := "second"
	_, err = managed.ListTools(read, &cursor, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 8, source.calls.Load(), "each cursor remains a distinct source page")
	_, err = managed.ListTools(read, nil, mcpclient.WithNoRetry(), mcpclient.WithJsonRpcRequestId(int64(77)))
	require.NoError(t, err)
	require.EqualValues(t, 9, source.calls.Load(), "custom request IDs bypass the snapshot")
	_, err = managed.ListTools(read, nil, mcpclient.WithNoRetry(), mcpclient.WithJsonRpcVersion("2.0"))
	require.NoError(t, err)
	require.EqualValues(t, 10, source.calls.Load(), "custom protocol version bypasses the snapshot")
}

func TestNoRetrySnapshotNewFetchDoesNotInheritOuterSnapshot(t *testing.T) {
	_, _, source, managed, base := noRetrySnapshotFixture(t)
	outer := WithToolCatalogReadSnapshot(base)
	_, err := managed.ListTools(outer, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	_, err = managed.ListTools(WithNewToolCatalogReadSnapshot(outer), nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 2, source.calls.Load(), "a new datasource fetch cannot inherit an outer tool-surface snapshot")
}

type failingNoRetryCatalog struct {
	stubClient
	calls atomic.Int32
	err   error
}

type cancelNoRetryCatalog struct {
	stubClient
	calls   atomic.Int32
	entered chan struct{}
}

func (s *cancelNoRetryCatalog) ListTools(ctx context.Context, _ *string, _ ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "read"}}, CacheScope: schema.ListToolsResultCacheScopePrivate, TtlMs: 0}, nil
}

func (s *failingNoRetryCatalog) ListTools(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListToolsResult, error) {
	s.calls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	return &schema.ListToolsResult{Tools: []schema.Tool{{Name: "read"}}, CacheScope: schema.ListToolsResultCacheScopePrivate, TtlMs: 0}, nil
}

func TestNoRetrySnapshotNeverCachesSourceError(t *testing.T) {
	m, _, _, managed, base := noRetrySnapshotFixture(t)
	source := &failingNoRetryCatalog{err: errors.New("source unavailable")}
	managed.entry = &entry{client: source}
	read := WithToolCatalogReadSnapshot(base)
	_, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
	require.Error(t, err)
	source.err = nil
	_, err = managed.ListTools(read, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 2, source.calls.Load())
	m.mu.Lock()
	require.Empty(t, m.toolCatalog, "NoRetry snapshot does not populate the manager's retained cache")
	m.mu.Unlock()
}

func TestNoRetrySnapshotCancellationDoesNotReplayOrPoisonRead(t *testing.T) {
	_, _, _, managed, base := noRetrySnapshotFixture(t)
	source := &cancelNoRetryCatalog{entered: make(chan struct{})}
	managed.entry = &entry{client: source}
	read := WithToolCatalogReadSnapshot(base)
	first, cancel := context.WithCancel(read)
	done := make(chan error, 1)
	go func() {
		_, err := managed.ListTools(first, nil, mcpclient.WithNoRetry())
		done <- err
	}()
	select {
	case <-source.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("NoRetry source was never called")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.EqualValues(t, 1, source.calls.Load(), "NoRetry must not reconnect or replay the source")
	_, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 2, source.calls.Load(), "a canceled source cannot poison the operation-local snapshot")
}

func TestNoRetrySnapshotNotificationDuringSourceReadCannotPublishStalePage(t *testing.T) {
	m, _, source, managed, base := noRetrySnapshotFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	source.onList = func() {
		if !first.Swap(true) {
			close(entered)
			<-release
		}
	}
	read := WithToolCatalogReadSnapshot(base)
	done := make(chan error, 1)
	go func() {
		_, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no-retry source did not enter")
	}
	(&changeHandler{manager: m, server: "server"}).OnNotification(read, &jsonrpc.Notification{Method: "notifications/tools/list_changed"})
	close(release)
	require.Error(t, <-done, "a changed producer catalog cannot publish its in-flight old page")
	_, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 2, source.calls.Load())
}

func TestNoRetrySnapshotCredentialRotationWhileSourceReadsIsIsolated(t *testing.T) {
	_, _, source, managed, base := noRetrySnapshotFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	source.onList = func() {
		if !first.Swap(true) {
			close(entered)
			<-release
		}
	}
	read := WithToolCatalogReadSnapshot(authctx.WithBearer(base, "token-one"))
	done := make(chan error, 1)
	go func() {
		_, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("original credential did not reach the source")
	}
	_, err := managed.ListTools(authctx.WithBearer(read, "token-two"), nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	close(release)
	require.NoError(t, <-done)
	require.EqualValues(t, 2, source.calls.Load(), "a changed credential cannot join an in-flight old credential")
}

func TestNoRetrySnapshotCancelingOneWaiterDoesNotCancelSource(t *testing.T) {
	_, _, source, managed, base := noRetrySnapshotFixture(t)
	entered, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
	source.onList = func() {
		close(entered)
		<-release
	}
	var lookups atomic.Int32
	read := observe.WithObserver(WithToolCatalogReadSnapshot(base), observe.ObserverFunc(func(event observe.Event) {
		if event.Phase == "mcp.tools.list.read_snapshot" && event.Cache == observe.CacheMiss && lookups.Add(1) == 2 {
			close(joined)
		}
	}))
	sourceDone := make(chan error, 1)
	go func() {
		_, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
		sourceDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("source did not enter")
	}
	waiterCtx, cancel := context.WithCancel(read)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := managed.ListTools(waiterCtx, nil, mcpclient.WithNoRetry())
		waiterDone <- err
	}()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not join the snapshot")
	}
	cancel()
	require.ErrorIs(t, <-waiterDone, context.Canceled)
	close(release)
	require.NoError(t, <-sourceDone)
	_, err := managed.ListTools(read, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	require.EqualValues(t, 1, source.calls.Load(), "canceling a waiter does not cancel or replay another caller's source")
}
