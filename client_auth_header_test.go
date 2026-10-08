package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type blockingReadCloser struct {
	started      chan struct{}
	closed       chan struct{}
	finished     chan struct{}
	startOnce    sync.Once
	closeOnce    sync.Once
	finishedOnce sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{
		started:  make(chan struct{}),
		closed:   make(chan struct{}),
		finished: make(chan struct{}),
	}
}

func (b *blockingReadCloser) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	b.finishedOnce.Do(func() { close(b.finished) })
	return 0, io.ErrClosedPipe
}

func (b *blockingReadCloser) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestWrapContextAuthHTTPClient_InjectsBearerHeaderFromContext(t *testing.T) {
	var seenAuth string
	client := wrapContextAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	})

	req, err := http.NewRequestWithContext(
		context.WithValue(context.Background(), authtransport.ContextAuthTokenKey, "token-123"),
		http.MethodGet,
		"http://example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}

	if _, err = client.Do(req); err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	if seenAuth != "Bearer token-123" {
		t.Fatalf("Authorization = %q, want %q", seenAuth, "Bearer token-123")
	}
}

func TestBFFUseIDToken_PerCallAuthTokenReachesToolAuthorizationHeader(t *testing.T) {
	var toolAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		switch request.Method {
		case schema.MethodInitialize:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "bff-id-token-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]interface{}{
					"protocolVersion": schema.LegacyProtocolVersion,
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "auth-probe", "version": "1.0"},
				},
			})
		case schema.MethodNotificationInitialized:
			w.WriteHeader(http.StatusAccepted)
		case schema.MethodToolsCall:
			toolAuthorization = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result":  map[string]interface{}{"content": []interface{}{}},
			})
		default:
			http.Error(w, "unexpected method", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	useBFF := true
	options := &ClientOptions{
		Name:            "steward",
		Version:         "1.0",
		ProtocolVersion: schema.LegacyProtocolVersion,
		Transport:       ClientTransport{Type: "streamable", ClientTransportHTTP: ClientTransportHTTP{URL: server.URL}},
		Auth:            &ClientAuth{BackendForFrontend: &useBFF, UseIdToken: true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	_, err = cli.CallTool(ctx, &schema.CallToolRequestParams{Name: "ResourceAuthorization"}, mcpclient.WithAuthToken("id-token-123"))
	if err != nil {
		t.Fatal(err)
	}
	if toolAuthorization != "Bearer id-token-123" {
		t.Fatalf("Authorization = %q, want %q", toolAuthorization, "Bearer id-token-123")
	}
}

func TestWrapContextAuthHTTPClient_PreservesExistingAuthorizationHeader(t *testing.T) {
	var seenAuth string
	client := wrapContextAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	})

	req, err := http.NewRequestWithContext(
		context.WithValue(context.Background(), authtransport.ContextAuthTokenKey, "token-123"),
		http.MethodGet,
		"http://example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer existing")

	if _, err = client.Do(req); err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	if seenAuth != "Bearer existing" {
		t.Fatalf("Authorization = %q, want %q", seenAuth, "Bearer existing")
	}
}

func TestWrapSSEProtocolRejectionHTTPClient_PreservesContextAuthAndOrdinaryBadRequest(t *testing.T) {
	var seenAuth string
	client := wrapSSEProtocolRejectionHTTPClient(wrapContextAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader("ordinary bad request")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}))

	req, err := http.NewRequestWithContext(
		context.WithValue(context.Background(), authtransport.ContextAuthTokenKey, "token-123"),
		http.MethodGet,
		"http://example.com/sse",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if seenAuth != "Bearer token-123" {
		t.Fatalf("Authorization = %q, want %q", seenAuth, "Bearer token-123")
	}
	if actual := string(body); actual != "ordinary bad request" {
		t.Fatalf("response body = %q, want %q", actual, "ordinary bad request")
	}
}

func TestWrapSSEProtocolRejectionHTTPClient_BlockingBadRequestBodyTimesOut(t *testing.T) {
	body := newBlockingReadCloser()
	client := wrapSSEProtocolRejectionHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       body,
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/sse", nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}

	startedAt := time.Now()
	response, err := client.Do(req)
	if response != nil {
		_ = response.Body.Close()
		t.Fatalf("expected nil response after body scan timeout, got status %d", response.StatusCode)
	}
	if !errors.Is(err, errProtocolRejectionScan) {
		t.Fatalf("expected body scan timeout, got %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 2*time.Second {
		t.Fatalf("blocking body scan returned too slowly: %v", elapsed)
	}
	if isProtocolVersionNegotiationError(err) {
		t.Fatalf("body scan timeout was classified as protocol negotiation: %v", err)
	}
	select {
	case <-body.started:
	default:
		t.Fatal("blocking response body was never read")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("blocking response body was not closed")
	}
	select {
	case <-body.finished:
	case <-time.After(time.Second):
		t.Fatal("response body read did not exit")
	}
}

func TestWrapSSEProtocolRejectionHTTPClient_BlockingBadRequestBodyHonorsCancellation(t *testing.T) {
	body := newBlockingReadCloser()
	client := wrapSSEProtocolRejectionHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       body,
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelTimer := time.AfterFunc(25*time.Millisecond, cancel)
	defer cancelTimer.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/sse", nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}

	startedAt := time.Now()
	response, err := client.Do(req)
	if response != nil {
		_ = response.Body.Close()
		t.Fatalf("expected nil response after cancellation, got status %d", response.StatusCode)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("cancelled body scan returned too slowly: %v", elapsed)
	}
	if isProtocolVersionNegotiationError(err) {
		t.Fatalf("body scan cancellation was classified as protocol negotiation: %v", err)
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("cancelled response body was not closed")
	}
	select {
	case <-body.finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled response body read did not exit")
	}
}
