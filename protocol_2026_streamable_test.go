package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	pclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	mcpserver "github.com/viant/mcp/server"
)

type julyProtocolClientHandler struct {
	notifications chan string
}

func (*julyProtocolClientHandler) Notify(context.Context, *jsonrpc.Notification) error { return nil }
func (*julyProtocolClientHandler) NextRequestID() jsonrpc.RequestId                    { return 1 }
func (*julyProtocolClientHandler) LastRequestID() jsonrpc.RequestId                    { return 1 }
func (*julyProtocolClientHandler) Implements(string) bool                              { return false }
func (*julyProtocolClientHandler) Init(context.Context, *schema.ClientCapabilities)    {}
func (h *julyProtocolClientHandler) OnNotification(_ context.Context, notification *jsonrpc.Notification) {
	if h.notifications == nil {
		return
	}
	select {
	case h.notifications <- notification.Method:
	default:
	}
}
func (*julyProtocolClientHandler) ListRoots(context.Context, *jsonrpc.TypedRequest[*schema.ListRootsRequest]) (*schema.ListRootsResult, *jsonrpc.Error) {
	return &schema.ListRootsResult{}, nil
}
func (*julyProtocolClientHandler) CreateMessage(context.Context, *jsonrpc.TypedRequest[*schema.CreateMessageRequest]) (*schema.CreateMessageResult, *jsonrpc.Error) {
	return &schema.CreateMessageResult{}, nil
}
func (*julyProtocolClientHandler) Elicit(context.Context, *jsonrpc.TypedRequest[*schema.ElicitRequest]) (*schema.ElicitResult, *jsonrpc.Error) {
	return &schema.ElicitResult{Action: schema.ElicitResultActionDecline}, nil
}

var _ pclient.Handler = (*julyProtocolClientHandler)(nil)

type protocolRequestObservation struct {
	method      string
	header      string
	bodyVersion string
}

type protocolRequestLog struct {
	mu           sync.Mutex
	observations []protocolRequestObservation
}

func (l *protocolRequestLog) append(observation protocolRequestObservation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observations = append(l.observations, observation)
}

func (l *protocolRequestLog) snapshot() []protocolRequestObservation {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]protocolRequestObservation(nil), l.observations...)
}

func requestBodyProtocolVersion(method string, params json.RawMessage) string {
	var values map[string]json.RawMessage
	if json.Unmarshal(params, &values) != nil {
		return ""
	}
	if method == schema.MethodInitialize {
		var version string
		_ = json.Unmarshal(values["protocolVersion"], &version)
		return version
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(values["_meta"], &meta) != nil {
		return ""
	}
	var version string
	_ = json.Unmarshal(meta["io.modelcontextprotocol/protocolVersion"], &version)
	return version
}

func assertProtocolRequestOrder(t *testing.T, actual, expected []protocolRequestObservation) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("expected %d protocol requests, got %d: %#v", len(expected), len(actual), actual)
	}
	for i := range expected {
		if actual[i] != expected[i] {
			t.Fatalf("request %d: expected %#v, got %#v", i, expected[i], actual[i])
		}
	}
}

func TestUnversionedClientFallsBackToJuneProtocolOnVersionNegotiationFailure(t *testing.T) {
	testCases := []struct {
		name                 string
		writeLegacyRejection func(http.ResponseWriter, json.RawMessage)
	}{
		{
			name: "old server header error",
			writeLegacyRejection: func(w http.ResponseWriter, _ json.RawMessage) {
				http.Error(w, "invalid MCP-Protocol-Version", http.StatusBadRequest)
			},
		},
		{
			name: "JSON-RPC unsupported version",
			writeLegacyRejection: func(w http.ResponseWriter, id json.RawMessage) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"jsonrpc": "2.0",
					"id":      id,
					"error": map[string]interface{}{
						"code":    schema.ErrorCodeUnsupportedProtocolVersion,
						"message": "unsupported protocol version",
					},
				})
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var requests protocolRequestLog
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				defer r.Body.Close()
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests.append(protocolRequestObservation{
					method:      request.Method,
					header:      r.Header.Get(schema.HeaderProtocolVersion),
					bodyVersion: requestBodyProtocolVersion(request.Method, request.Params),
				})

				switch request.Method {
				case schema.MethodServerDiscover:
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"jsonrpc": "2.0",
						"id":      request.ID,
						"error": map[string]interface{}{
							"code":    jsonrpc.MethodNotFound,
							"message": "method not found",
						},
					})
				case schema.MethodInitialize:
					switch r.Header.Get(schema.HeaderProtocolVersion) {
					case schema.LegacyProtocolVersion:
						testCase.writeLegacyRejection(w, request.ID)
					case compatibilityProtocolVersionJune2025:
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Mcp-Session-Id", "june-session")
						_ = json.NewEncoder(w).Encode(map[string]interface{}{
							"jsonrpc": "2.0",
							"id":      request.ID,
							"result": map[string]interface{}{
								"protocolVersion": compatibilityProtocolVersionJune2025,
								"capabilities":    map[string]interface{}{},
								"serverInfo": map[string]interface{}{
									"name":    "june-server",
									"version": "1.0",
								},
							},
						})
					default:
						t.Errorf("unexpected initialize protocol %q", r.Header.Get(schema.HeaderProtocolVersion))
						http.Error(w, "unexpected protocol", http.StatusBadRequest)
					}
				case schema.MethodNotificationInitialized:
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Errorf("unexpected method %q", request.Method)
					http.Error(w, "unexpected method", http.StatusBadRequest)
				}
			}))
			defer server.Close()

			options := &ClientOptions{
				Name:    "auto-client",
				Version: "1.0",
				Transport: ClientTransport{
					Type: "streamable",
					ClientTransportHTTP: ClientTransportHTTP{
						URL: server.URL,
					},
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()

			if options.ProtocolVersion != compatibilityProtocolVersionJune2025 {
				t.Fatalf("expected fallback to %s, got %s", compatibilityProtocolVersionJune2025, options.ProtocolVersion)
			}
			assertProtocolRequestOrder(t, requests.snapshot(), []protocolRequestObservation{
				{method: schema.MethodServerDiscover, header: schema.LatestProtocolVersion, bodyVersion: schema.LatestProtocolVersion},
				{method: schema.MethodInitialize, header: schema.LegacyProtocolVersion, bodyVersion: schema.LegacyProtocolVersion},
				{method: schema.MethodInitialize, header: compatibilityProtocolVersionJune2025, bodyVersion: compatibilityProtocolVersionJune2025},
				{method: schema.MethodNotificationInitialized, header: compatibilityProtocolVersionJune2025},
			})
		})
	}
}

func TestUnversionedClientDoesNotTryJuneForNonProtocolFailure(t *testing.T) {
	var requests protocolRequestLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests.append(protocolRequestObservation{
			method:      request.Method,
			header:      r.Header.Get(schema.HeaderProtocolVersion),
			bodyVersion: requestBodyProtocolVersion(request.Method, request.Params),
		})
		if request.Method == schema.MethodServerDiscover {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"error": map[string]interface{}{
					"code":    jsonrpc.MethodNotFound,
					"message": "method not found",
				},
			})
			return
		}
		http.Error(w, "server exploded", http.StatusInternalServerError)
	}))
	defer server.Close()

	options := &ClientOptions{
		Name:    "auto-client",
		Version: "1.0",
		Transport: ClientTransport{
			Type: "streamable",
			ClientTransportHTTP: ClientTransportHTTP{
				URL: server.URL,
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
	if client != nil {
		client.Close()
		t.Fatalf("expected nil client on automatic protocol failure, got %T", client)
	}
	if err == nil {
		t.Fatal("expected automatic protocol attempts to fail")
	}
	if !strings.Contains(err.Error(), schema.LatestProtocolVersion) ||
		!strings.Contains(err.Error(), schema.LegacyProtocolVersion) ||
		!strings.Contains(err.Error(), "server exploded") {
		t.Fatalf("expected aggregated latest and legacy errors, got %v", err)
	}
	assertProtocolRequestOrder(t, requests.snapshot(), []protocolRequestObservation{
		{method: schema.MethodServerDiscover, header: schema.LatestProtocolVersion, bodyVersion: schema.LatestProtocolVersion},
		{method: schema.MethodInitialize, header: schema.LegacyProtocolVersion, bodyVersion: schema.LegacyProtocolVersion},
	})
}

func TestExplicitProtocolDoesNotFallback(t *testing.T) {
	testCases := []struct {
		version string
		method  string
	}{
		{version: schema.LatestProtocolVersion, method: schema.MethodServerDiscover},
		{version: schema.LegacyProtocolVersion, method: schema.MethodInitialize},
		{version: compatibilityProtocolVersionJune2025, method: schema.MethodInitialize},
	}
	for _, testCase := range testCases {
		t.Run(testCase.version, func(t *testing.T) {
			var requests protocolRequestLog
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				var request struct {
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests.append(protocolRequestObservation{
					method:      request.Method,
					header:      r.Header.Get(schema.HeaderProtocolVersion),
					bodyVersion: requestBodyProtocolVersion(request.Method, request.Params),
				})
				http.Error(w, invalidMCPProtocolVersionMarker, http.StatusBadRequest)
			}))
			defer server.Close()

			options := &ClientOptions{
				Name:            "explicit-client",
				Version:         "1.0",
				ProtocolVersion: testCase.version,
				Transport: ClientTransport{
					Type: "streamable",
					ClientTransportHTTP: ClientTransportHTTP{
						URL: server.URL,
					},
				},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
			if client != nil {
				client.Close()
				t.Fatalf("expected nil client on explicit protocol failure, got %T", client)
			}
			if err == nil {
				t.Fatal("expected explicit protocol initialization to fail")
			}
			if options.ProtocolVersion != testCase.version {
				t.Fatalf("explicit protocol changed to %q", options.ProtocolVersion)
			}
			assertProtocolRequestOrder(t, requests.snapshot(), []protocolRequestObservation{
				{method: testCase.method, header: testCase.version, bodyVersion: testCase.version},
			})
		})
	}
}

func TestProtocolVersionNegotiationErrorClassification(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "JSON-RPC unsupported version", err: jsonrpc.NewError(schema.ErrorCodeUnsupportedProtocolVersion, "unsupported protocol version", nil), expected: true},
		{name: "SSE marker sentinel", err: errInvalidMCPProtocolVersion, expected: true},
		{name: "streamable HTTP 400 marker", err: jsonrpc.NewInternalError("invalid status code: 400: invalid MCP-Protocol-Version", nil), expected: true},
		{name: "bare marker", err: errors.New("invalid MCP-Protocol-Version")},
		{name: "bare marker in JSON-RPC error", err: jsonrpc.NewInternalError("invalid MCP-Protocol-Version", nil)},
		{name: "authentication", err: jsonrpc.NewUnauthorizedError(http.StatusUnauthorized, []byte("login required"))},
		{name: "network", err: errors.New("dial tcp: connection refused")},
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "arbitrary 5xx", err: errors.New("invalid status code: 500: invalid MCP-Protocol-Version")},
		{name: "malformed response", err: errors.New("failed to unmarshal InitializeResult")},
		{name: "other JSON-RPC error", err: jsonrpc.NewInternalError("server failed", nil)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := isProtocolVersionNegotiationError(testCase.err); actual != testCase.expected {
				t.Fatalf("expected %t, got %t for %v", testCase.expected, actual, testCase.err)
			}
		})
	}
}

func TestUnversionedClientFallsBackToLegacyProtocol(t *testing.T) {
	var discoverCalls, initializeCalls int
	legacyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case schema.MethodServerDiscover:
			discoverCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"error": map[string]interface{}{
					"code":    -32601,
					"message": "method not found",
				},
			})
		case schema.MethodInitialize:
			initializeCalls++
			w.Header().Set("Mcp-Session-Id", "legacy-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]interface{}{
					"protocolVersion": schema.LegacyProtocolVersion,
					"capabilities":    map[string]interface{}{},
					"serverInfo": map[string]interface{}{
						"name":    "legacy-server",
						"version": "1.0",
					},
				},
			})
		case schema.MethodNotificationInitialized:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer legacyServer.Close()

	options := &ClientOptions{
		Name:    "auto-client",
		Version: "1.0",
		Transport: ClientTransport{
			Type: "streamable",
			ClientTransportHTTP: ClientTransportHTTP{
				URL: legacyServer.URL,
			},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if options.ProtocolVersion != schema.LegacyProtocolVersion {
		t.Fatalf("expected fallback to %s, got %s", schema.LegacyProtocolVersion, options.ProtocolVersion)
	}
	if discoverCalls != 1 || initializeCalls != 1 {
		t.Fatalf("expected one discovery and one legacy initialize, got discovery=%d initialize=%d", discoverCalls, initializeCalls)
	}
}

func TestJulyStreamableHTTPIsStatelessEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	newHandler := serverproto.WithDefaultHandler(ctx, func(*serverproto.DefaultHandler) error { return nil })
	server, err := mcpserver.New(
		mcpserver.WithNewHandler(newHandler),
		mcpserver.WithImplementation(schema.Implementation{Name: "july-server", Version: "1.0"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := server.HTTP(ctx, listener.Addr().String())
	go func() { _ = httpServer.Serve(listener) }()
	defer httpServer.Close()

	options := &ClientOptions{
		Name:            "july-client",
		Version:         "1.0",
		ProtocolVersion: schema.LatestProtocolVersion,
		Transport: ClientTransport{
			Type: "streamable",
			ClientTransportHTTP: ClientTransportHTTP{
				URL: "http://" + listener.Addr().String() + "/mcp",
			},
		},
	}
	clientHandler := &julyProtocolClientHandler{notifications: make(chan string, 1)}
	client, err := NewClientWithContext(ctx, clientHandler, options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	ping, err := client.Ping(ctx, &schema.PingRequestParams{})
	if err != nil {
		t.Fatal(err)
	}
	if ping == nil {
		t.Fatal("expected ping result")
	}

	listenCtx, stopListening := context.WithCancel(ctx)
	listenError := make(chan error, 1)
	go func() {
		_, err := client.Listen(listenCtx, schema.SubscriptionFilter{})
		listenError <- err
	}()
	select {
	case method := <-clientHandler.notifications:
		if method != schema.MethodNotificationSubscriptionsAcknowledged {
			t.Fatalf("unexpected subscription notification %q", method)
		}
	case <-ctx.Done():
		t.Fatal("subscription acknowledgement was not received")
	}
	stopListening()
	select {
	case err := <-listenError:
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("expected cancelled subscription, got %v", err)
		}
	case <-ctx.Done():
		t.Fatal("subscription request did not stop after cancellation")
	}
	if _, err := client.Ping(ctx, &schema.PingRequestParams{}); err != nil {
		t.Fatalf("client was poisoned by subscription cancellation: %v", err)
	}
}
