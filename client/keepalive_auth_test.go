package client

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/schema"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

func TestKeepaliveReconnectRetainsOnlySelectedCredential(t *testing.T) {
	for _, version := range []string{schema.LegacyProtocolVersion, schema.LatestProtocolVersion} {
		for _, token := range []string{"fixture-alice-token", "fixture-bob-token", ""} {
			t.Run(version+"/"+token, func(t *testing.T) {
				type requestValue struct{}
				var mu sync.Mutex
				var authenticated []bool
				reconnects := make(chan struct{}, 8)
				peer := &mockTransport{send: func(ctx context.Context, request *jsonrpc.Request) (*jsonrpc.Response, error) {
					if request.Method == schema.MethodPing {
						require.Nil(t, ctx.Value(requestValue{}), "keepalive must not retain the request context")
						return nil, errors.New("synthetic keepalive transport failure")
					}
					got, _ := ctx.Value(authtransport.ContextAuthTokenKey).(string)
					mu.Lock()
					authenticated = append(authenticated, got == token)
					mu.Unlock()
					extensions := map[string]any{}
					if token != "" && got == token {
						extensions[schema.SkillsExtension] = map[string]any{}
					}
					result := map[string]any{"protocolVersion": version, "capabilities": map[string]any{"extensions": extensions}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}
					if request.Method == schema.MethodServerDiscover {
						result = map[string]any{"supportedVersions": []string{version}, "capabilities": map[string]any{"extensions": extensions}, "resultType": "complete", "cacheScope": "private", "ttlMs": 0}
					}
					raw, _ := json.Marshal(result)
					return &jsonrpc.Response{Jsonrpc: jsonrpc.Version, Id: request.Id, Result: raw}, nil
				}}
				connection := New("fixture", "1", peer, WithProtocolVersion(version), WithPingInterval(5*time.Millisecond), WithReconnect(func(ctx context.Context) (transport.Transport, error) {
					reconnects <- struct{}{}
					return peer, nil
				}))
				defer connection.Close()
				ctx, cancel := context.WithCancel(context.WithValue(context.Background(), requestValue{}, "request-only"))
				var options []RequestOption
				if token == "fixture-bob-token" {
					options = []RequestOption{WithAuthToken(token)}
				} else {
					ctx = context.WithValue(ctx, authtransport.ContextAuthTokenKey, token)
				}
				_, err := connection.Initialize(ctx, options...)
				require.NoError(t, err)
				cancel()
				for range 2 {
					select {
					case <-reconnects:
					case <-time.After(time.Second):
						t.Fatal("keepalive did not reconnect")
					}
				}
				require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(authenticated) >= 3 }, time.Second, time.Millisecond)
				connection.Close()
				mu.Lock()
				defer mu.Unlock()
				for _, ok := range authenticated {
					require.True(t, ok, "background reconnect discarded its negotiated credential")
				}
				state, err := connection.NegotiatedServer()
				require.NoError(t, err)
				_, present := state.Capabilities.Extensions[schema.SkillsExtension]
				require.Equal(t, token != "", present)
			})
		}
	}
}

func TestCloseCancelsBlockedKeepalive(t *testing.T) {
	started := make(chan struct{})
	peer := &mockTransport{send: func(ctx context.Context, _ *jsonrpc.Request) (*jsonrpc.Response, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	connection := New("fixture", "1", peer, WithPingInterval(time.Millisecond))
	connection.initialized = true
	connection.startPinger(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("keepalive did not start")
	}
	closed := make(chan struct{})
	go func() {
		connection.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the blocked keepalive")
	}
	connection.Close()
}
