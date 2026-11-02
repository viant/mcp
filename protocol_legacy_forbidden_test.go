package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

func TestAutomaticHTTPNegotiatesBeforeSendingVersionHeader(t *testing.T) {
	for _, selected := range []string{compatibilityProtocolVersionJune2025, schema.LegacyProtocolVersion, "2099-01-01"} {
		t.Run(selected, func(t *testing.T) {
			var mu sync.Mutex
			var methods []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(405)
					return
				}
				defer r.Body.Close()
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params json.RawMessage `json:"params"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					w.WriteHeader(400)
					return
				}
				if r.Header.Get("Authorization") != "Bearer synthetic-credential" {
					t.Error("lost caller credential")
					w.WriteHeader(403)
					return
				}
				mu.Lock()
				methods = append(methods, request.Method)
				mu.Unlock()
				if request.Method == schema.MethodServerDiscover {
					http.Error(w, "access denied", http.StatusForbidden)
					return
				}
				if request.Method == schema.MethodInitialize {
					// A legacy gate rejects newer headers before inspecting the body, and
					// may echo any body proposal. The baseline avoids a false negotiation.
					if r.Header.Get(schema.HeaderProtocolVersion) != "" {
						http.Error(w, "access denied", 403)
						return
					}
					if got := requestBodyProtocolVersion(request.Method, request.Params); got != compatibilityProtocolVersionJune2025 {
						t.Errorf("initial offer=%s", got)
					}
					w.Header().Set("Mcp-Session-Id", "synthetic-session")
				} else if r.Header.Get(schema.HeaderProtocolVersion) != selected {
					t.Errorf("selected version header=%q; want %s", r.Header.Get(schema.HeaderProtocolVersion), selected)
				}
				if request.Method == schema.MethodNotificationInitialized {
					w.WriteHeader(202)
					return
				}
				var result any
				switch request.Method {
				case schema.MethodInitialize:
					result = map[string]any{"protocolVersion": selected, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "example-peer", "version": "1"}}
				case schema.MethodToolsList:
					result = map[string]any{"tools": []any{}}
				default:
					t.Errorf("unexpected method %s", request.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = context.WithValue(ctx, authtransport.ContextAuthTokenKey, "synthetic-credential")
			bff := selected == compatibilityProtocolVersionJune2025
			options := &ClientOptions{Name: "example", Version: "1", Auth: &ClientAuth{BackendForFrontend: &bff, UseIdToken: bff}, Transport: ClientTransport{Type: "streamable", ClientTransportHTTP: ClientTransportHTTP{URL: server.URL}}}
			cli, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
			if cli != nil {
				defer cli.Close()
			}
			if selected == "2099-01-01" {
				if err == nil {
					t.Fatal("unsupported reply was accepted")
				}
				mu.Lock()
				defer mu.Unlock()
				if len(methods) != 1 {
					t.Fatalf("unsupported reply emitted subsequent requests: %v", methods)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := cli.NegotiatedServer()
			if err != nil || result.ProtocolVersion != selected || options.ProtocolVersion != selected {
				t.Fatalf("negotiated selection: %+v %v", result, err)
			}
			if _, err = cli.ListTools(ctx, nil); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			expected := []string{schema.MethodInitialize, schema.MethodNotificationInitialized, schema.MethodToolsList}
			if len(methods) != len(expected) {
				t.Fatalf("methods=%v", methods)
			}
			for i := range expected {
				if methods[i] != expected[i] {
					t.Fatalf("methods=%v", methods)
				}
			}
		})
	}
}

func TestAutomaticHTTPDenialDoesNotProbeVersions(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "access denied", status) }))
			defer server.Close()
			bff := false
			options := &ClientOptions{Name: "example", Version: "1", Auth: &ClientAuth{BackendForFrontend: &bff}, Transport: ClientTransport{Type: "streamable", ClientTransportHTTP: ClientTransportHTTP{URL: server.URL}}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cli, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
			if cli != nil {
				cli.Close()
			}
			if err == nil || calls != 1 {
				t.Fatalf("denial must be terminal: calls=%d err=%v", calls, err)
			}
			if status == 401 && !jsonrpc.IsUnauthorized(err) {
				t.Fatalf("lost unauthorized classification: %v", err)
			}
			if status == 403 {
				var httpErr *jsonrpc.HTTPStatusError
				if !errors.As(err, &httpErr) || httpErr.StatusCode != 403 {
					t.Fatalf("lost forbidden: %v", err)
				}
			}
		})
	}
}

func TestAutomaticHTTPDiscoversOnlyAfterTypedLifecycleRejection(t *testing.T) {
	var requests protocolRequestLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		defer r.Body.Close()
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		requests.append(protocolRequestObservation{method: request.Method, header: r.Header.Get(schema.HeaderProtocolVersion), bodyVersion: requestBodyProtocolVersion(request.Method, request.Params)})
		w.Header().Set("Content-Type", "application/json")
		if request.Method == schema.MethodInitialize {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": jsonrpc.MethodNotFound, "message": "initialize is not supported"}})
			return
		}
		result := map[string]any{"resultType": "complete", "cacheScope": "public", "ttlMs": 0, "supportedVersions": []string{schema.LatestProtocolVersion}, "capabilities": map[string]any{}}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	options := &ClientOptions{Name: "example", Version: "1", Transport: ClientTransport{Type: "streamable", ClientTransportHTTP: ClientTransportHTTP{URL: server.URL}}}
	cli, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if options.ProtocolVersion != schema.LatestProtocolVersion {
		t.Fatalf("version=%s", options.ProtocolVersion)
	}
	assertProtocolRequestOrder(t, requests.snapshot(), []protocolRequestObservation{{method: schema.MethodInitialize, bodyVersion: compatibilityProtocolVersionJune2025}, {method: schema.MethodServerDiscover, header: schema.LatestProtocolVersion, bodyVersion: schema.LatestProtocolVersion}})
}

func TestAutomaticHTTPReconnectKeepsNegotiatedVersionAndAuthTransport(t *testing.T) {
	var requests protocolRequestLog
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		defer r.Body.Close()
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-credential" {
			t.Error("reconnect lost credential")
			w.WriteHeader(403)
			return
		}
		requests.append(protocolRequestObservation{method: request.Method, header: r.Header.Get(schema.HeaderProtocolVersion), bodyVersion: requestBodyProtocolVersion(request.Method, request.Params)})
		if request.Method == schema.MethodNotificationInitialized {
			w.WriteHeader(202)
			return
		}
		var result any = map[string]any{"tools": []any{}}
		if request.Method == schema.MethodInitialize {
			w.Header().Set("Mcp-Session-Id", "synthetic-session")
			result = map[string]any{"protocolVersion": schema.LegacyProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "example-peer", "version": "1"}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer server.Close()
	var once sync.Once
	authRT, err := authtransport.New(authtransport.WithTransport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		fail := false
		if req.Header.Get(schema.HeaderMethod) == schema.MethodToolsList {
			once.Do(func() { fail = true })
		}
		if fail {
			return nil, errors.New("session 'synthetic' not found")
		}
		return http.DefaultTransport.RoundTrip(req)
	})))
	if err != nil {
		t.Fatal(err)
	}
	options := &ClientOptions{Name: "example", Version: "1", Transport: ClientTransport{Type: "streamable", ClientTransportHTTP: ClientTransportHTTP{URL: server.URL}}}
	httpClient := &http.Client{Transport: authRT}
	options.SetAuthTransport(authRT, httpClient)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, authtransport.ContextAuthTokenKey, "synthetic-credential")
	cli, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err = cli.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if options.cachedAuthRT != authRT || options.cachedHTTPClient != httpClient {
		t.Fatal("reconnect replaced shared authentication transport")
	}
	assertProtocolRequestOrder(t, requests.snapshot(), []protocolRequestObservation{
		{method: schema.MethodInitialize, bodyVersion: compatibilityProtocolVersionJune2025},
		{method: schema.MethodNotificationInitialized, header: schema.LegacyProtocolVersion},
		{method: schema.MethodInitialize, bodyVersion: schema.LegacyProtocolVersion},
		{method: schema.MethodNotificationInitialized, header: schema.LegacyProtocolVersion},
		{method: schema.MethodToolsList, header: schema.LegacyProtocolVersion},
	})
}
