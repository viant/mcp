package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

// Synthetic HTTP server: a restart discards its first session. The status body
// deliberately has no session-error prose; recovery must use typed HTTP status.
func TestHTTPMetadataSessionRecovery(t *testing.T) {
	for _, tc := range []struct {
		name                                                                 string
		status                                                               int
		stateless, rpcMissing, write, noRetry, alwaysFail, canceled, timeout bool
		parallel                                                             int
		starts, calls                                                        int64
	}{
		{name: "negotiated session expires", status: 404, starts: 2, calls: 2},
		{name: "missing endpoint without session", status: 404, stateless: true, starts: 1, calls: 1},
		{name: "RPC method not found", rpcMissing: true, starts: 1, calls: 1},
		{name: "expired session rejects tool before execution", status: 404, write: true, starts: 2, calls: 2},
		{name: "explicit no retry", status: 404, noRetry: true, starts: 1, calls: 1},
		{name: "tool no retry remains authoritative", status: 404, write: true, noRetry: true, starts: 1, calls: 1},
		{name: "tool RPC method missing is not replayed", rpcMissing: true, write: true, starts: 1, calls: 1},
		{name: "accepted tool response timeout is not replayed", write: true, timeout: true, starts: 1, calls: 1},
		{name: "forbidden is not session expiry", status: 403, starts: 1, calls: 1},
		{name: "unavailable is not session expiry", status: 503, starts: 1, calls: 1},
		{name: "second expiry is bounded", status: 404, alwaysFail: true, starts: 2, calls: 2},
		{name: "concurrent expired readers share recovery", status: 404, parallel: 2, starts: 2, calls: 4},
		{name: "canceled read is not replayed", status: 404, canceled: true, starts: 1, calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var starts, calls, toolExecutions atomic.Int64
			duration := 5 * time.Second
			if tc.timeout {
				duration = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			ctx = context.WithValue(ctx, authtransport.ContextAuthTokenKey, "synthetic-session-token")
			firstAttempts := int64(1)
			if tc.parallel > 1 {
				firstAttempts = int64(tc.parallel)
			}
			expiredReaders := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				require.Equal(t, "Bearer synthetic-session-token", r.Header.Get("Authorization"))
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
				reply := func(result any) {
					w.Header().Set("Content-Type", "application/json")
					require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}))
				}
				switch request.Method {
				case schema.MethodServerDiscover:
					starts.Add(1)
					reply(map[string]any{"resultType": "complete", "cacheScope": "public", "ttlMs": 0, "capabilities": map[string]any{}, "supportedVersions": []string{schema.LatestProtocolVersion}})
				case schema.MethodInitialize:
					require.Empty(t, r.Header.Get("Mcp-Session-Id"), "new initialization must not carry the discarded session")
					n := starts.Add(1)
					w.Header().Set("Mcp-Session-Id", fmt.Sprintf("session-%d", n))
					reply(map[string]any{"protocolVersion": schema.LegacyProtocolVersion, "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "example", "version": "1"}})
				case schema.MethodNotificationInitialized:
					require.Equal(t, schema.LegacyProtocolVersion, r.Header.Get(schema.HeaderProtocolVersion))
					w.WriteHeader(http.StatusAccepted)
				case schema.MethodToolsList, schema.MethodToolsCall:
					n := calls.Add(1)
					if !tc.stateless {
						require.Equal(t, schema.LegacyProtocolVersion, r.Header.Get(schema.HeaderProtocolVersion))
						require.Equal(t, fmt.Sprintf("session-%d", starts.Load()), r.Header.Get("Mcp-Session-Id"))
					}
					if tc.rpcMissing {
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": jsonrpc.MethodNotFound, "message": "method absent"}})
						return
					}
					if tc.timeout {
						toolExecutions.Add(1)
						<-r.Context().Done()
						return
					}
					if tc.canceled {
						cancel()
					}
					if n <= firstAttempts || tc.alwaysFail {
						if tc.parallel > 1 {
							if n == firstAttempts {
								close(expiredReaders)
							}
							select {
							case <-expiredReaders:
							case <-r.Context().Done():
								return
							}
						}
						http.Error(w, "endpoint unavailable", tc.status)
						return
					}
					if request.Method == schema.MethodToolsCall {
						toolExecutions.Add(1)
						reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "executed once"}}})
					} else {
						reply(map[string]any{"tools": []any{map[string]any{"name": "example", "inputSchema": map[string]string{"type": "object"}}}})
					}
				default:
					t.Errorf("unexpected method %s", request.Method)
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			bff := true
			options := &ClientOptions{Name: "session-check", Version: "1", Auth: &ClientAuth{BackendForFrontend: &bff}, Transport: ClientTransport{Type: "streamable", ClientTransportHTTP: ClientTransportHTTP{URL: server.URL}}, PingIntervalSeconds: 3600}
			if tc.stateless {
				options.ProtocolVersion = schema.LatestProtocolVersion
			}
			c, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, options)
			require.NoError(t, err)
			defer c.Close()
			var opts []client.RequestOption
			if tc.noRetry {
				opts = append(opts, client.WithNoRetry())
			}
			if tc.parallel > 1 {
				results := make(chan error, tc.parallel)
				for i := 0; i < tc.parallel; i++ {
					go func() { _, e := c.ListTools(ctx, nil, opts...); results <- e }()
				}
				for i := 0; i < tc.parallel; i++ {
					require.NoError(t, <-results)
				}
			} else if tc.write {
				_, err = c.CallTool(ctx, &schema.CallToolRequestParams{Name: "write-example"}, opts...)
			} else {
				_, err = c.ListTools(ctx, nil, opts...)
			}
			if tc.starts == 2 && !tc.alwaysFail {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, tc.starts, starts.Load())
			require.Equal(t, tc.calls, calls.Load())
			if tc.write && (tc.starts == 2 || tc.timeout) {
				require.EqualValues(t, 1, toolExecutions.Load())
			} else {
				require.Zero(t, toolExecutions.Load())
			}
		})
	}
}
