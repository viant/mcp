package manager

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	mcpcfg "github.com/viant/mcp/client/config"
)

type concurrencyProviderStub struct{}

func (c *concurrencyProviderStub) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{}, nil
}

type stubClient struct{}

func (s *stubClient) Initialize(ctx context.Context, options ...mcpclient.RequestOption) (*mcpschema.InitializeResult, error) {
	return &mcpschema.InitializeResult{}, nil
}
func (s *stubClient) ListResourceTemplates(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourceTemplatesResult, error) {
	return nil, nil
}
func (s *stubClient) ListResources(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListResourcesResult, error) {
	return nil, nil
}
func (s *stubClient) ListPrompts(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListPromptsResult, error) {
	return nil, nil
}
func (s *stubClient) ListTools(ctx context.Context, cursor *string, options ...mcpclient.RequestOption) (*mcpschema.ListToolsResult, error) {
	return &mcpschema.ListToolsResult{}, nil
}
func (s *stubClient) ReadResource(ctx context.Context, params *mcpschema.ReadResourceRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ReadResourceResult, error) {
	return nil, nil
}
func (s *stubClient) GetPrompt(ctx context.Context, params *mcpschema.GetPromptRequestParams, options ...mcpclient.RequestOption) (*mcpschema.GetPromptResult, error) {
	return nil, nil
}
func (s *stubClient) CallTool(ctx context.Context, params *mcpschema.CallToolRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CallToolResult, error) {
	return &mcpschema.CallToolResult{}, nil
}
func (s *stubClient) Complete(ctx context.Context, params *mcpschema.CompleteRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CompleteResult, error) {
	return nil, nil
}
func (s *stubClient) Ping(ctx context.Context, params *mcpschema.PingRequestParams, options ...mcpclient.RequestOption) (*mcpschema.PingResult, error) {
	return nil, nil
}
func (s *stubClient) Subscribe(ctx context.Context, params *mcpschema.SubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SubscribeResult, error) {
	return nil, nil
}
func (s *stubClient) Unsubscribe(ctx context.Context, params *mcpschema.UnsubscribeRequestParams, options ...mcpclient.RequestOption) (*mcpschema.UnsubscribeResult, error) {
	return nil, nil
}
func (s *stubClient) SetLevel(ctx context.Context, params *mcpschema.SetLevelRequestParams, options ...mcpclient.RequestOption) (*mcpschema.SetLevelResult, error) {
	return nil, nil
}
func (s *stubClient) ListRoots(ctx context.Context, params *mcpschema.ListRootsRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ListRootsResult, error) {
	return nil, nil
}
func (s *stubClient) CreateMessage(ctx context.Context, params *mcpschema.CreateMessageRequestParams, options ...mcpclient.RequestOption) (*mcpschema.CreateMessageResult, error) {
	return nil, nil
}
func (s *stubClient) Elicit(ctx context.Context, params *mcpschema.ElicitRequestParams, options ...mcpclient.RequestOption) (*mcpschema.ElicitResult, error) {
	return nil, nil
}

func TestManagerGet_SingleFlightsConcurrentClientCreation(t *testing.T) {
	mgr, err := New(&concurrencyProviderStub{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var creates atomic.Int32
	expected := &stubClient{}
	mgr.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) {
		creates.Add(1)
		time.Sleep(50 * time.Millisecond)
		return expected, nil
	}

	const workers = 8
	var wg sync.WaitGroup
	results := make([]mcpclient.Interface, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = mgr.Get(context.Background(), "conv-1", "primary")
		}(i)
	}
	wg.Wait()

	if got := creates.Load(); got != 1 {
		t.Fatalf("newClient called %d times, want 1", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Get(%d) error = %v", i, err)
		}
		if results[i] == nil {
			t.Fatalf("Get(%d) returned nil client", i)
		}
		if results[i] != results[0] {
			t.Fatalf("Get(%d) client mismatch: got %p want %p", i, results[i], results[0])
		}
	}
	if _, err := results[0].ListTools(context.Background(), nil); err != nil {
		t.Fatalf("managed client ListTools() error = %v", err)
	}
	if expected == nil {
		t.Fatalf("expected raw client should remain allocated")
	}
}
