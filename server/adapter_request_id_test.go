package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp-protocol/syncmap"
	"github.com/viant/mcp/client"
)

type adapterRequestIDHandler struct {
	*serverproto.DefaultHandler

	mu            sync.Mutex
	ids           []uint64
	waitForCancel bool
	entered       chan struct{}
	enteredOnce   sync.Once
}

func (h *adapterRequestIDHandler) Implements(method string) bool {
	if method == schema.MethodToolsCall {
		return true
	}
	if h.DefaultHandler == nil {
		return false
	}
	return h.DefaultHandler.Implements(method)
}

func (h *adapterRequestIDHandler) CallTool(ctx context.Context, request *jsonrpc.TypedRequest[*schema.CallToolRequest]) (*schema.CallToolResult, *jsonrpc.Error) {
	h.mu.Lock()
	h.ids = append(h.ids, request.Id)
	h.mu.Unlock()

	if h.entered != nil {
		h.enteredOnce.Do(func() {
			close(h.entered)
		})
	}
	if h.waitForCancel {
		<-ctx.Done()
	}

	return &schema.CallToolResult{
		Content: []schema.CallToolResultContentElem{
			schema.TextContent{Type: "text", Text: "ok"},
		},
	}, nil
}

func (h *adapterRequestIDHandler) requestIDs() []uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()

	ids := make([]uint64, len(h.ids))
	copy(ids, h.ids)
	return ids
}

func newRequestIDAdapter(handler serverproto.Handler) *Adapter {
	return NewAdapter(&Handler{
		Server: &Server{
			activeContexts: syncmap.NewMap[int, *activeContext](),
		},
		handler:        handler,
		clientFeatures: make(map[string]bool),
	})
}

func TestAdapterConcurrentCallToolAssignsUniqueRequestIDs(t *testing.T) {
	handler := &adapterRequestIDHandler{}
	adapter := newRequestIDAdapter(handler)

	const calls = 64
	start := make(chan struct{})
	errs := make(chan error, calls)
	var wg sync.WaitGroup
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := adapter.CallTool(context.Background(), &schema.CallToolRequestParams{Name: "test"})
			errs <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("CallTool returned error: %v", err)
		}
	}

	ids := handler.requestIDs()
	if len(ids) != calls {
		t.Fatalf("got %d request IDs, want %d", len(ids), calls)
	}

	seen := make(map[uint64]bool, calls)
	for _, id := range ids {
		if id == 0 {
			t.Fatalf("got zero request ID in %v", ids)
		}
		if seen[id] {
			t.Fatalf("duplicate request ID %d in %v", id, ids)
		}
		seen[id] = true
	}
}

func TestAdapterExplicitRequestIDAdvancesNextAutomaticID(t *testing.T) {
	handler := &adapterRequestIDHandler{}
	adapter := newRequestIDAdapter(handler)
	ctx := context.Background()

	if _, err := adapter.CallTool(ctx, &schema.CallToolRequestParams{Name: "test"}, client.WithJsonRpcRequestId(41)); err != nil {
		t.Fatalf("explicit CallTool returned error: %v", err)
	}
	if _, err := adapter.CallTool(ctx, &schema.CallToolRequestParams{Name: "test"}); err != nil {
		t.Fatalf("automatic CallTool returned error: %v", err)
	}

	ids := handler.requestIDs()
	want := []uint64{41, 42}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("got request IDs %v, want %v", ids, want)
	}
}

func TestAdapterCallToolParentContextCancellationReachesHandler(t *testing.T) {
	handler := &adapterRequestIDHandler{
		waitForCancel: true,
		entered:       make(chan struct{}),
	}
	adapter := newRequestIDAdapter(handler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := adapter.CallTool(ctx, &schema.CallToolRequestParams{Name: "test"})
		done <- err
	}()

	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("CallTool did not reach handler")
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CallTool returned error after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CallTool did not finish after parent context cancellation")
	}

	ids := handler.requestIDs()
	if len(ids) != 1 {
		t.Fatalf("got %d request IDs, want 1", len(ids))
	}
	if ids[0] == 0 {
		t.Fatal("handler saw zero request ID")
	}
}
