package manager

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

type lifecycleClient struct {
	stubClient
	closed  atomic.Int32
	started chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (c *lifecycleClient) CallTool(ctx context.Context, params *mcpschema.CallToolRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	if c.started != nil {
		select {
		case <-c.started:
		default:
			close(c.started)
		}
	}
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.done != nil {
		close(c.done)
	}
	return &mcpschema.CallToolResult{}, nil
}

func (c *lifecycleClient) Close() error {
	c.closed.Add(1)
	return nil
}

func waitClosed(t *testing.T, client *lifecycleClient, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := client.closed.Load(); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("client close count = %d, want %d", client.closed.Load(), want)
}

func TestManagerReap_DoesNotCloseActiveClient(t *testing.T) {
	mgr, err := New(&concurrencyProviderStub{}, WithTTL(time.Nanosecond))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	raw := &lifecycleClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	mgr.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) {
		return raw, nil
	}
	cli, err := mgr.Get(context.Background(), "conv-active", "primary")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	go func() {
		_, _ = cli.CallTool(context.Background(), &mcpschema.CallToolRequestParams{})
	}()
	<-raw.started
	time.Sleep(time.Millisecond)
	mgr.Reap()
	if got := raw.closed.Load(); got != 0 {
		t.Fatalf("active client was closed %d times", got)
	}
	close(raw.release)
	<-raw.done
	time.Sleep(time.Millisecond)
	mgr.Reap()
	waitClosed(t, raw, 1)
}

func TestManagerReap_RecreatesAfterIdleClientClosed(t *testing.T) {
	mgr, err := New(&concurrencyProviderStub{}, WithTTL(time.Nanosecond))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var creates atomic.Int32
	var first *lifecycleClient
	mgr.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) {
		client := &lifecycleClient{}
		if creates.Add(1) == 1 {
			first = client
		}
		return client, nil
	}
	cli1, err := mgr.Get(context.Background(), "conv-idle", "primary")
	if err != nil {
		t.Fatalf("first Get() error = %v", err)
	}
	if _, err := cli1.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	time.Sleep(time.Millisecond)
	mgr.Reap()
	waitClosed(t, first, 1)
	cli2, err := mgr.Get(context.Background(), "conv-idle", "primary")
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}
	if cli2 == cli1 {
		t.Fatalf("expected fresh managed client after reap")
	}
	if got := creates.Load(); got != 2 {
		t.Fatalf("create count = %d, want 2", got)
	}
}

func TestManagerReconnect_ClosesActiveOldClientAfterRelease(t *testing.T) {
	mgr, err := New(&concurrencyProviderStub{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var creates atomic.Int32
	old := &lifecycleClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	fresh := &lifecycleClient{}
	mgr.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) {
		if creates.Add(1) == 1 {
			return old, nil
		}
		return fresh, nil
	}
	cli, err := mgr.Get(context.Background(), "conv-reconnect", "primary")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	go func() {
		_, _ = cli.CallTool(context.Background(), &mcpschema.CallToolRequestParams{})
	}()
	<-old.started
	reconnected, err := mgr.Reconnect(context.Background(), "conv-reconnect", "primary")
	if err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}
	if reconnected == cli {
		t.Fatalf("Reconnect() returned old managed client")
	}
	if got := old.closed.Load(); got != 0 {
		t.Fatalf("active old client closed early %d times", got)
	}
	close(old.release)
	<-old.done
	waitClosed(t, old, 1)
}

func TestManagerCloseConversation_RemovesMatchingClientsOnly(t *testing.T) {
	mgr, err := New(&concurrencyProviderStub{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	clients := map[string]*lifecycleClient{}
	mgr.newClientFn = func(_ context.Context, convID, serverName string) (mcpclient.Interface, error) {
		client := &lifecycleClient{}
		clients[convID+"|"+serverName] = client
		return client, nil
	}
	if _, err := mgr.Get(context.Background(), "conv-delete", "primary"); err != nil {
		t.Fatalf("Get(delete) error = %v", err)
	}
	if _, err := mgr.Get(context.Background(), "conv-keep", "primary"); err != nil {
		t.Fatalf("Get(keep) error = %v", err)
	}
	if _, err := mgr.Get(context.Background(), "mcp-discovery:primary:background", "primary"); err != nil {
		t.Fatalf("Get(background) error = %v", err)
	}
	mgr.CloseConversation("conv-delete")
	waitClosed(t, clients["conv-delete|primary"], 1)
	if got := clients["conv-keep|primary"].closed.Load(); got != 0 {
		t.Fatalf("other conversation close count = %d, want 0", got)
	}
	if got := clients["mcp-discovery:primary:background|primary"].closed.Load(); got != 0 {
		t.Fatalf("background close count = %d, want 0", got)
	}
}

func TestManagerCloseConversation_DiscardsInflightCreation(t *testing.T) {
	mgr, err := New(&concurrencyProviderStub{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	raw := &lifecycleClient{}
	mgr.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) {
		close(started)
		<-release
		return raw, nil
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := mgr.Get(context.Background(), "conv-inflight", "primary")
		errCh <- err
	}()
	<-started
	mgr.CloseConversation("conv-inflight")
	close(release)
	if err := <-errCh; err == nil {
		t.Fatalf("expected inflight Get() to fail after CloseConversation")
	}
	waitClosed(t, raw, 1)
}
