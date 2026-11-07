package manager

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp"
	protocolclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protocolserver "github.com/viant/mcp-protocol/server"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	mcpcfg "github.com/viant/mcp/client/config"
	mcpserver "github.com/viant/mcp/server"
)

// Reproduce with a compiled binary to avoid Go build/cache-trim contention:
// go test -c -o /tmp/manager-tools-bench.test ./protocol/mcp/manager
// /tmp/manager-tools-bench.test -test.run '^$' -test.bench '^BenchmarkMCPTools' -test.benchmem -test.benchtime=1x -test.count=3
// Servers run before timers start. Cold includes fresh Manager/SDK clients;
// listing excludes handshake/setup; warm HTTP uses the genuine page cache,
// while registered in-memory catalogs intentionally remain uncached.
// Payload counters exclude JSON-RPC/HTTP envelopes and are precomputed, so
// counting them does not add an extra timed serialization. Injected latency
// applies to response-producing RPCs, not one-way notifications.
// Server-controlled opaque cursors are deliberately followed sequentially.
// A client cannot request an offset/limit or enumerate future pages in parallel.
type listBenchStats struct{ list, control, payload atomic.Int64 }
type listBenchHandler struct {
	*protocolserver.DefaultHandler
	tools        []schema.Tool
	page         int
	cursors      map[string]int
	next         map[int]string
	delay        time.Duration
	stats        *listBenchStats
	private      bool
	payloadBytes map[int]int64
	capability   string
}

func (h *listBenchHandler) Implements(method string) bool {
	if method == "tools/list" {
		return true
	}
	if h.capability == "resources" && (method == "resources/list" || method == "resources/read" || method == "resources/templates/list") {
		return true
	}
	if h.capability == "prompts" && (method == "prompts/list" || method == "prompts/get") {
		return true
	}
	return h.DefaultHandler.Implements(method)
}
func (h *listBenchHandler) Initialize(ctx context.Context, in *schema.InitializeRequestParams, out *schema.InitializeResult) {
	h.stats.control.Add(1)
	time.Sleep(h.delay)
	h.DefaultHandler.Initialize(ctx, in, out)
	h.capabilities(&out.Capabilities)
}
func (h *listBenchHandler) Discover(ctx context.Context, out *schema.DiscoverResult) {
	h.stats.control.Add(1)
	time.Sleep(h.delay)
	h.DefaultHandler.Discover(ctx, out)
	h.capabilities(&out.Capabilities)
}
func (h *listBenchHandler) capabilities(out *schema.ServerCapabilities) {
	out.Tools = &schema.ServerCapabilitiesTools{}
	switch h.capability {
	case "resources":
		out.Resources = &schema.ServerCapabilitiesResources{}
	case "prompts":
		out.Prompts = &schema.ServerCapabilitiesPrompts{}
	case "notifications":
		changed := true
		out.Tools.ListChanged = &changed
	}
}
func (h *listBenchHandler) ListTools(ctx context.Context, request *jsonrpc.TypedRequest[*schema.ListToolsRequest]) (*schema.ListToolsResult, *jsonrpc.Error) {
	h.stats.list.Add(1)
	if h.delay > 0 {
		timer := time.NewTimer(h.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, jsonrpc.NewInternalError("benchmark canceled", nil)
		}
	}
	start := 0
	if cursor := request.Request.Params.Cursor; cursor != nil {
		var ok bool
		start, ok = h.cursors[*cursor]
		if !ok {
			return nil, jsonrpc.NewInvalidParamsError("unknown cursor", nil)
		}
	}
	end := start + h.page
	if end > len(h.tools) {
		end = len(h.tools)
	}
	out := &schema.ListToolsResult{Tools: h.tools[start:end], TtlMs: 60000, CacheScope: schema.ListToolsResultCacheScopePublic}
	if h.private {
		out.CacheScope = schema.ListToolsResultCacheScopePrivate
	}
	if end < len(h.tools) {
		token := h.next[end]
		out.NextCursor = &token
	}
	h.stats.payload.Add(h.payloadBytes[start])
	return out, nil
}

type listBenchProvider struct{ configs map[string]*mcpcfg.MCPClient }

func (p *listBenchProvider) Names(context.Context) ([]string, error) {
	out := []string{}
	for name := range p.configs {
		out = append(out, name)
	}
	return out, nil
}
func (p *listBenchProvider) Options(_ context.Context, name string) (*mcpcfg.MCPClient, error) {
	return p.configs[name], nil
}

type listBenchFixture struct {
	configs   map[string]*mcpcfg.MCPClient
	factories map[string]func(context.Context) (mcpclient.Interface, error)
	stats     []*listBenchStats
	names     []string
	servers   []*httptest.Server
}

// Every server has 100 tools: 80 schemas with eight input/output properties,
// and 20 wide tools with exactly 200 properties in EACH input and output schema.
func representativeBenchTools(tb testing.TB, name string) []schema.Tool {
	tb.Helper()
	type rangeInput struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	sample := struct {
		AccountID      string            `json:"accountId"`
		Query          string            `json:"query"`
		Range          rangeInput        `json:"range"`
		GroupBy        []string          `json:"groupBy"`
		Limit          int               `json:"limit"`
		Offset         int               `json:"offset"`
		Filters        map[string]string `json:"filters"`
		IncludeMetrics bool              `json:"includeMetrics"`
	}{}
	var normal schema.ToolInputSchema
	if err := normal.Load(sample); err != nil {
		tb.Fatal(err)
	}
	normalRaw, err := json.Marshal(normal)
	if err != nil {
		tb.Fatal(err)
	}
	var normalOutput schema.ToolOutputSchema
	if err := json.Unmarshal(normalRaw, &normalOutput); err != nil {
		tb.Fatal(err)
	}
	properties := map[string]interface{}{}
	for i := 0; i < 200; i++ {
		properties[fmt.Sprintf("field_%03d", i)] = map[string]interface{}{"type": "number", "description": fmt.Sprintf("Analytic dimension or metric %03d with a stable schema field.", i)}
	}
	wideRaw, err := json.Marshal(map[string]interface{}{"type": "object", "properties": properties})
	if err != nil {
		tb.Fatal(err)
	}
	var wide schema.ToolInputSchema
	var wideOutput schema.ToolOutputSchema
	if err := json.Unmarshal(wideRaw, &wide); err != nil {
		tb.Fatal(err)
	}
	if err := json.Unmarshal(wideRaw, &wideOutput); err != nil {
		tb.Fatal(err)
	}
	description := "Read account analytics with date range, filters, grouped metrics and bounded rows."
	tools := make([]schema.Tool, 100)
	for i := range tools {
		input, output := normal, &normalOutput
		if i%5 == 0 {
			input, output = wide, &wideOutput
		}
		tools[i] = schema.Tool{Name: fmt.Sprintf("%s_tool_%03d", name, i), Description: &description, InputSchema: input, OutputSchema: output, Meta: map[string]interface{}{"fixture": map[string]interface{}{"category": "analytics", "version": 1}}}
	}
	return tools
}
func newListBenchFixture(tb testing.TB, transportName string, page, count int, delay time.Duration, mixed bool) *listBenchFixture {
	tb.Helper()
	fixture := &listBenchFixture{configs: map[string]*mcpcfg.MCPClient{}, factories: map[string]func(context.Context) (mcpclient.Interface, error){}}
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("provider-%d", i)
		actualPage := page
		capability := "tools"
		if mixed {
			if i == 0 {
				actualPage = 100
			}
			if i == 2 {
				capability = "resources"
			}
			if i == 3 {
				capability = "prompts"
			}
			if i == 4 {
				capability = "notifications"
			}
		}
		tools := representativeBenchTools(tb, name)
		stats := &listBenchStats{}
		cursors := map[string]int{}
		next := map[int]string{}
		for position := actualPage; position < 100; position += actualPage {
			sum := sha256.Sum256([]byte(fmt.Sprintf("opaque/%s/%d/%d", name, page, position)))
			token := base64.RawURLEncoding.EncodeToString(sum[:])
			cursors[token] = position
			next[position] = token
		}
		payloadBytes := map[int]int64{}
		for start := 0; start < 100; start += actualPage {
			end := start + actualPage
			if end > 100 {
				end = 100
			}
			result := &schema.ListToolsResult{Tools: tools[start:end], TtlMs: 60000, CacheScope: schema.ListToolsResultCacheScopePublic}
			if transportName == "memory" {
				result.CacheScope = schema.ListToolsResultCacheScopePrivate
			}
			if end < 100 {
				token := next[end]
				result.NextCursor = &token
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				tb.Fatal(err)
			}
			payloadBytes[start] = int64(len(encoded))
		}
		server, err := mcpserver.New(mcpserver.WithImplementation(schema.Implementation{Name: name, Version: "1"}), mcpserver.WithStreamableURI("/mcp"), mcpserver.WithRootRedirect(false), mcpserver.WithNewHandler(func(_ context.Context, n transport.Notifier, l logger.Logger, c protocolclient.Operations) (protocolserver.Handler, error) {
			return &listBenchHandler{DefaultHandler: protocolserver.NewDefaultHandler(n, l, c), tools: tools, page: actualPage, cursors: cursors, next: next, delay: delay, stats: stats, private: transportName == "memory", payloadBytes: payloadBytes, capability: capability}, nil
		}))
		if err != nil {
			tb.Fatal(err)
		}
		fixture.names = append(fixture.names, name)
		fixture.stats = append(fixture.stats, stats)
		if transportName == "http" {
			server.UseStreamableHTTP(true)
			httpServer := httptest.NewServer(server.HTTP(context.Background(), "").Handler)
			fixture.servers = append(fixture.servers, httpServer)
			fixture.configs[name] = &mcpcfg.MCPClient{ToolsListVisibility: "public", ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: httpServer.URL + "/mcp"}}}}
		} else {
			fixture.configs[name] = &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: name}
			fixture.factories[name] = func(ctx context.Context) (mcpclient.Interface, error) { return server.AsClient(ctx), nil }
		}
	}
	tb.Cleanup(func() {
		for _, server := range fixture.servers {
			server.Close()
		}
	})
	return fixture
}
func (f *listBenchFixture) manager(tb testing.TB) *Manager {
	tb.Helper()
	var m *Manager
	var err error
	if len(f.factories) == 0 {
		m, err = New(&listBenchProvider{configs: f.configs})
	} else {
		m, err = New(nil)
	}
	if err != nil {
		tb.Fatal(err)
	}
	for name, factory := range f.factories {
		if err := m.RegisterLocal(context.Background(), name, f.configs[name], factory); err != nil {
			tb.Fatal(err)
		}
	}
	return m
}
func (f *listBenchFixture) counts() (int64, int64, int64) {
	var list, control, payload int64
	for _, stats := range f.stats {
		list += stats.list.Load()
		control += stats.control.Load()
		payload += stats.payload.Load()
	}
	return list, control, payload
}
func listBenchContext() context.Context {
	return authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "benchmark-user"})
}
func enumerateBenchTools(ctx context.Context, client mcpclient.Interface) error {
	var cursor *string
	total := 0
	wide := 0
	seen := map[string]bool{}
	for {
		out, err := client.ListTools(ctx, cursor)
		if err != nil {
			return err
		}
		if out == nil {
			return fmt.Errorf("nil tools page")
		}
		total += len(out.Tools)
		for _, tool := range out.Tools {
			if len(tool.InputSchema.Properties) == 200 {
				if tool.OutputSchema == nil || len(tool.OutputSchema.Properties) != 200 {
					return fmt.Errorf("wide output schema mismatch")
				}
				wide++
			}
		}
		if out.NextCursor == nil || *out.NextCursor == "" {
			break
		}
		if seen[*out.NextCursor] {
			return fmt.Errorf("repeated cursor")
		}
		seen[*out.NextCursor] = true
		cursor = out.NextCursor
	}
	if total != 100 || wide != 20 {
		return fmt.Errorf("listed %d tools/%d wide, want100/20", total, wide)
	}
	return nil
}
func runBenchProviders(ctx context.Context, m *Manager, names []string, workers int) error {
	jobs := make(chan string, len(names))
	for _, name := range names {
		jobs <- name
	}
	close(jobs)
	failures := make(chan error, len(names))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				client, err := m.Get(ctx, "benchmark", name)
				if err == nil {
					err = enumerateBenchTools(ctx, client)
				}
				if err != nil {
					failures <- err
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		return err
	}
	return nil
}
func BenchmarkMCPToolsList(b *testing.B) {
	for _, transportName := range []string{"memory", "http"} {
		for _, page := range []int{10, 20, 100} {
			for _, delay := range []time.Duration{0, 20 * time.Millisecond} {
				for _, mode := range []string{"cold", "listing", "warm"} {
					b.Run(fmt.Sprintf("%s/page=%d/latency=%s/%s", transportName, page, delay, mode), func(b *testing.B) {
						b.StopTimer()
						fixture := newListBenchFixture(b, transportName, page, 1, delay, false)
						ctx := listBenchContext()
						name := fixture.names[0]
						var warm *Manager
						var warmClient mcpclient.Interface
						if mode == "warm" {
							warm = fixture.manager(b)
							var err error
							warmClient, err = warm.Get(ctx, "benchmark", name)
							if err != nil {
								b.Fatal(err)
							}
							if err := enumerateBenchTools(ctx, warmClient); err != nil {
								b.Fatal(err)
							}
							defer warm.CloseConversation("benchmark")
						}
						list0, control0, bytes0 := fixture.counts()
						b.ReportAllocs()
						for i := 0; i < b.N; i++ {
							if mode == "warm" {
								b.StartTimer()
								err := enumerateBenchTools(ctx, warmClient)
								b.StopTimer()
								if err != nil {
									b.Fatal(err)
								}
								continue
							}
							if mode == "cold" {
								b.StartTimer()
							}
							m := fixture.manager(b)
							client, err := m.Get(ctx, "benchmark", name)
							if err != nil {
								b.Fatal(err)
							}
							if mode == "listing" {
								b.StartTimer()
							}
							err = enumerateBenchTools(ctx, client)
							b.StopTimer()
							m.CloseConversation("benchmark")
							if err != nil {
								b.Fatal(err)
							}
						}
						list, control, payload := fixture.counts()
						b.ReportMetric(float64(list-list0)/float64(b.N), "list-req/op")
						if mode == "listing" {
							b.ReportMetric(float64(control-control0)/float64(b.N), "setup-control-req/op")
						} else {
							b.ReportMetric(float64(control-control0)/float64(b.N), "control-req/op")
						}
						b.ReportMetric(float64(payload-bytes0)/float64(b.N), "list-payload-B/op")
					})
				}
			}
		}
	}
}
func BenchmarkMCPToolsProviders(b *testing.B) {
	for _, transportName := range []string{"memory", "http"} {
		for _, page := range []int{10, 20, 100} {
			for _, delay := range []time.Duration{0, 20 * time.Millisecond} {
				for _, workers := range []int{1, 4} {
					for _, mode := range []string{"cold", "listing", "warm"} {
						b.Run(fmt.Sprintf("%s/providers=5/page=%d/latency=%s/workers=%d/%s", transportName, page, delay, workers, mode), func(b *testing.B) { runProviderBench(b, transportName, page, delay, workers, mode, false) })
					}
				}
			}
		}
	}
}

// One tools-only server is deliberately unpaginated; other four follow page.
// Server2 exposes resources, server3 prompts, server4 tools-change notifications.
func BenchmarkMCPToolsMixedCapabilities(b *testing.B) {
	for _, transportName := range []string{"memory", "http"} {
		for _, workers := range []int{1, 4} {
			for _, mode := range []string{"cold", "warm"} {
				b.Run(fmt.Sprintf("%s/providers=5/page=20/latency=0s/workers=%d/%s", transportName, workers, mode), func(b *testing.B) { runProviderBench(b, transportName, 20, 0, workers, mode, true) })
			}
		}
	}
}
func runProviderBench(b *testing.B, transportName string, page int, delay time.Duration, workers int, mode string, mixed bool) {
	b.StopTimer()
	fixture := newListBenchFixture(b, transportName, page, 5, delay, mixed)
	ctx := listBenchContext()
	var warm *Manager
	if mode == "warm" {
		warm = fixture.manager(b)
		if err := runBenchProviders(ctx, warm, fixture.names, workers); err != nil {
			b.Fatal(err)
		}
		defer warm.CloseConversation("benchmark")
	}
	list0, control0, bytes0 := fixture.counts()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := warm
		if mode == "cold" {
			b.StartTimer()
		}
		if mode != "warm" {
			m = fixture.manager(b)
		}
		if mode == "listing" {
			for _, name := range fixture.names {
				if _, err := m.Get(ctx, "benchmark", name); err != nil {
					b.Fatal(err)
				}
			}
		}
		if mode != "cold" {
			b.StartTimer()
		}
		err := runBenchProviders(ctx, m, fixture.names, workers)
		b.StopTimer()
		if mode != "warm" {
			m.CloseConversation("benchmark")
		}
		if err != nil {
			b.Fatal(err)
		}
	}
	list, control, payload := fixture.counts()
	b.ReportMetric(float64(list-list0)/float64(b.N), "list-req/op")
	b.ReportMetric(float64(payload-bytes0)/float64(b.N), "list-payload-B/op")
	if mode == "listing" {
		b.ReportMetric(float64(control-control0)/float64(b.N), "setup-control-req/op")
	} else {
		b.ReportMetric(float64(control-control0)/float64(b.N), "control-req/op")
	}
}
func TestMCPToolsListBenchmarkFixture(t *testing.T) {
	for _, transportName := range []string{"memory", "http"} {
		for _, page := range []int{10, 20, 100} {
			t.Run(fmt.Sprintf("%s/%d", transportName, page), func(t *testing.T) {
				fixture := newListBenchFixture(t, transportName, page, 1, 0, false)
				ctx := listBenchContext()
				m := fixture.manager(t)
				defer m.CloseConversation("benchmark")
				client, err := m.Get(ctx, "benchmark", fixture.names[0])
				if err != nil {
					t.Fatal(err)
				}
				if err := enumerateBenchTools(ctx, client); err != nil {
					t.Fatal(err)
				}
				list, _, _ := fixture.counts()
				if list != int64(100/page) {
					t.Fatalf("list requests %d", list)
				}
				if err := enumerateBenchTools(ctx, client); err != nil {
					t.Fatal(err)
				}
				after, _, _ := fixture.counts()
				if transportName == "http" && after != list {
					t.Fatalf("HTTP warm path dispatched%d new requests", after-list)
				}
				if transportName == "memory" && after != list*2 {
					t.Fatalf("in-memory path must remain uncached; requests%d want%d", after, list*2)
				}
			})
		}
	}
}
