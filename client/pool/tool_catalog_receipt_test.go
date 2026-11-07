package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp-protocol/schema"
	client "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	mcpcfg "github.com/viant/mcp/client/config"
	"github.com/viant/mcp/client/observe"
)

type receiptCatalogClient struct {
	catalogClient
	entered, release chan struct{}
	firstError       bool
	ttl              int
	onFirstReturn    func()
}

func (c *receiptCatalogClient) ListTools(ctx context.Context, _ *string, _ ...client.RequestOption) (*schema.ListToolsResult, error) {
	call := c.calls.Add(1)
	if call == 1 {
		close(c.entered)
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if c.onFirstReturn != nil {
			c.onFirstReturn()
		}
		if c.firstError {
			return nil, errors.New("first source failed")
		}
	}
	description := authctx.EffectiveUserID(ctx)
	return &schema.ListToolsResult{CacheScope: schema.ListToolsResultCacheScopePrivate, TtlMs: c.ttl, Tools: []schema.Tool{{Name: fmt.Sprintf("source-%d", call), Description: &description, Meta: map[string]any{"nested": map[string]any{"value": "original"}}}}}, nil
}

type receiptRead struct {
	result *schema.ListToolsResult
	err    error
}

func startReceiptRead(ctx context.Context, managed *managedClient) <-chan receiptRead {
	done := make(chan receiptRead, 1)
	go func() { result, err := managed.ListTools(ctx, nil); done <- receiptRead{result, err} }()
	return done
}
func receiptResult(t *testing.T, done <-chan receiptRead) receiptRead {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("catalog read did not complete")
		return receiptRead{}
	}
}
func receiptFixture(t *testing.T) (*Manager, *managedClient, *receiptCatalogClient, context.Context, func()) {
	t.Helper()
	m, _, _, managed, _ := catalogSetup(t, false)
	source := &receiptCatalogClient{entered: make(chan struct{}), release: make(chan struct{})}
	managed.entry.client = source
	var once sync.Once
	release := func() { once.Do(func() { close(source.release) }) }
	t.Cleanup(release)
	base, cancel := context.WithTimeout(authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"}), 5*time.Second)
	t.Cleanup(cancel)
	return m, managed, source, base, release
}
func waitingReceiptContext(t *testing.T, base context.Context, managed *managedClient) (context.Context, <-chan receiptRead) {
	t.Helper()
	entered := make(chan struct{}, 1)
	ctx := observe.WithObserver(WithToolCatalogReadSnapshot(base), observe.ObserverFunc(func(event observe.Event) {
		if event.Phase == "mcp.tools.list.read_snapshot" && event.Cache == observe.CacheMiss {
			select {
			case entered <- struct{}{}:
			default:
			}
		}
	}))
	done := startReceiptRead(ctx, managed)
	select {
	case <-entered:
	case <-base.Done():
		t.Fatal("waiter did not reach the pending catalog")
	}
	return ctx, done
}

func TestZeroTTLCatalogSharesOnlyAlreadyWaitingSourceCompletion(t *testing.T) {
	m, managed, source, base, release := receiptFixture(t)
	warm := WithToolCatalogReadSnapshot(base)
	first := startReceiptRead(warm, managed)
	<-source.entered
	foreground, second := waitingReceiptContext(t, base, managed)
	release()
	a, b := receiptResult(t, first), receiptResult(t, second)
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	require.EqualValues(t, 1, source.calls.Load(), "concurrent warm and foreground reads share the existing source singleflight")
	a.result.Tools[0].Meta["nested"].(map[string]any)["value"] = "caller mutation"
	require.Equal(t, "original", b.result.Tools[0].Meta["nested"].(map[string]any)["value"])
	for _, ctx := range []context.Context{warm, foreground} {
		_, err := managed.ListTools(ctx, nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, source.calls.Load(), "each joined operation receives its own existing read snapshot")
	m.mu.Lock()
	retained := len(m.toolCatalog)
	m.mu.Unlock()
	require.Zero(t, retained, "completed zero-TTL results must not remain in the shared cache")
	next, err := managed.ListTools(WithToolCatalogReadSnapshot(base), nil)
	require.NoError(t, err)
	require.Equal(t, "source-2", next.Tools[0].Name)
	require.EqualValues(t, 2, source.calls.Load(), "a later independent request must refetch")
}

func TestZeroTTLCatalogReceiptRejectsChangedAuthorityOrGeneration(t *testing.T) {
	for _, mode := range []string{"credential", "configuration", "notification", "source-error"} {
		t.Run(mode, func(t *testing.T) {
			m, managed, source, base, release := receiptFixture(t)
			jar, err := cookiejar.New(nil)
			require.NoError(t, err)
			endpoint, _ := url.Parse("https://example.test/mcp")
			jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "initial"}})
			m.cookieJar = jar
			source.firstError = mode == "source-error"
			first := startReceiptRead(WithToolCatalogReadSnapshot(base), managed)
			<-source.entered
			_, second := waitingReceiptContext(t, base, managed)
			switch mode {
			case "credential":
				jar.SetCookies(endpoint, []*http.Cookie{{Name: "session", Value: "rotated"}})
			case "configuration":
				m.prov.(*catalogProvider).cfg.PrimitiveProviderIdentity = "changed"
			case "notification":
				m.mu.Lock()
				m.invalidateToolCatalogLocked("server")
				m.mu.Unlock()
			}
			release()
			a, b := receiptResult(t, first), receiptResult(t, second)
			if mode == "source-error" {
				require.Error(t, a.err)
			} else {
				require.NoError(t, a.err)
			}
			if mode == "configuration" {
				require.ErrorContains(t, b.err, "configuration changed")
				require.Nil(t, b.result, "a held client must not return bytes or refetch a potentially replaced endpoint")
				require.EqualValues(t, 1, source.calls.Load())
				return
			}
			require.NoError(t, b.err)
			require.Equal(t, "source-2", b.result.Tools[0].Name, "waiter must get a fresh successful source rather than stale bytes or the first error")
			require.EqualValues(t, 2, source.calls.Load())
		})
	}
}

func TestZeroTTLCatalogWaiterCancellationDoesNotConsumeOrCancelSource(t *testing.T) {
	_, managed, source, base, release := receiptFixture(t)
	first := startReceiptRead(WithToolCatalogReadSnapshot(base), managed)
	<-source.entered
	waiter, cancel := context.WithCancel(base)
	_, second := waitingReceiptContext(t, waiter, managed)
	cancel()
	canceled := receiptResult(t, second)
	require.ErrorIs(t, canceled.err, context.Canceled)
	require.Nil(t, canceled.result)
	release()
	require.NoError(t, receiptResult(t, first).err)
	require.EqualValues(t, 1, source.calls.Load())
}

func TestZeroTTLCatalogConcurrentDifferentPrincipalDoesNotJoin(t *testing.T) {
	_, managed, source, base, release := receiptFixture(t)
	first := startReceiptRead(WithToolCatalogReadSnapshot(base), managed)
	<-source.entered
	bob := authctx.WithUserInfo(base, &authctx.UserInfo{Subject: "bob"})
	second := startReceiptRead(WithToolCatalogReadSnapshot(bob), managed)
	b := receiptResult(t, second)
	require.NoError(t, b.err)
	require.Equal(t, "bob", *b.result.Tools[0].Description)
	require.Equal(t, "source-2", b.result.Tools[0].Name)
	release()
	a := receiptResult(t, first)
	require.NoError(t, a.err)
	require.Equal(t, "alice", *a.result.Tools[0].Description)
	require.EqualValues(t, 2, source.calls.Load())
}

func TestZeroTTLCatalogCanceledSourceDoesNotPoisonWaitingOperation(t *testing.T) {
	_, managed, source, base, release := receiptFixture(t)
	sourceCtx, cancelSource := context.WithCancel(base)
	first := startReceiptRead(WithToolCatalogReadSnapshot(sourceCtx), managed)
	<-source.entered
	_, second := waitingReceiptContext(t, base, managed)
	cancelSource()
	canceled := receiptResult(t, first)
	require.ErrorIs(t, canceled.err, context.Canceled)
	result := receiptResult(t, second)
	require.NoError(t, result.err)
	require.Equal(t, "source-2", result.result.Tools[0].Name)
	require.EqualValues(t, 2, source.calls.Load(), "a canceled producer must not publish a receipt or cancel another operation")
	release()
}

func TestZeroTTLCatalogPrivateDowngradeCannotSharePublicInflightResult(t *testing.T) {
	m, managed, source, base, release := receiptFixture(t)
	m.prov.(*catalogProvider).cfg.ToolsListVisibility = "public"
	first := startReceiptRead(WithToolCatalogReadSnapshot(base), managed)
	<-source.entered
	bob := authctx.WithUserInfo(base, &authctx.UserInfo{Subject: "bob"})
	_, second := waitingReceiptContext(t, bob, managed)
	release()
	a, b := receiptResult(t, first), receiptResult(t, second)
	require.NoError(t, a.err)
	require.NoError(t, b.err)
	require.Equal(t, "alice", *a.result.Tools[0].Description)
	require.Equal(t, "bob", *b.result.Tools[0].Description)
	require.Equal(t, "source-2", b.result.Tools[0].Name, "a private producer response must not be shared by an initially public key")
	require.EqualValues(t, 2, source.calls.Load())
}

type receiptWaiterKey struct{}
type receiptRecheckProvider struct {
	*catalogProvider
	returned   <-chan struct{}
	rechecking chan struct{}
	allow      <-chan struct{}
	once       sync.Once
}

func (p *receiptRecheckProvider) Options(ctx context.Context, server string) (*mcpcfg.MCPClient, error) {
	if waiting, _ := ctx.Value(receiptWaiterKey{}).(bool); waiting {
		select {
		case <-p.returned:
			p.once.Do(func() { close(p.rechecking) })
			select {
			case <-p.allow:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		default:
		}
	}
	return p.catalogProvider.Options(ctx, server)
}

func TestCatalogCompletionReceiptDoesNotOutlivePositiveLease(t *testing.T) {
	m, managed, source, base, release := receiptFixture(t)
	source.ttl = 10
	returned, rechecking, allow := make(chan struct{}), make(chan struct{}), make(chan struct{})
	source.onFirstReturn = func() { close(returned) }
	provider := &receiptRecheckProvider{catalogProvider: m.prov.(*catalogProvider), returned: returned, rechecking: rechecking, allow: allow}
	m.prov = provider
	first := startReceiptRead(WithToolCatalogReadSnapshot(base), managed)
	<-source.entered
	waiter := context.WithValue(base, receiptWaiterKey{}, true)
	_, second := waitingReceiptContext(t, waiter, managed)
	release()
	require.NoError(t, receiptResult(t, first).err)
	select {
	case <-rechecking:
	case <-base.Done():
		t.Fatal("waiter did not recheck source scope")
	}
	// Expire the producer lease while the existing waiter is paused before its
	// release check. This avoids a timing-dependent sleep in the regression.
	m.mu.Lock()
	for _, page := range m.toolCatalog {
		if page.ready == nil {
			page.expires = time.Now().Add(-time.Second)
		}
	}
	m.mu.Unlock()
	close(allow)
	result := receiptResult(t, second)
	require.NoError(t, result.err)
	require.Equal(t, "source-2", result.result.Tools[0].Name)
	require.EqualValues(t, 2, source.calls.Load(), "positive producer TTL remains an expiry fence for queued readers")
}
