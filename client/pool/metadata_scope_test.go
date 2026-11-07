package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	authctx "github.com/viant/mcp/client/auth/link"
	cfg "github.com/viant/mcp/client/config"
)

func TestMetadataConnectionSharesGatewayTransportWithinUser(t *testing.T) {
	var initializes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		if request.Method != "initialize" {
			w.WriteHeader(202)
			return
		}
		session := initializes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", fmt.Sprint(session))
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "ordinary", "version": "1"}}})
	}))
	defer server.Close()
	provider := &catalogProvider{cfg: &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{ProtocolVersion: "2025-11-25", Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: server.URL}}}}}
	manager, err := New(provider)
	require.NoError(t, err)
	defer manager.CloseConversation("")
	defer manager.CloseConversation("execution")
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	gateway, err := manager.Get(alice, "", "server")
	require.NoError(t, err)
	for _, logicalScope := range []string{"discovery-first-conversation", "discovery-second-conversation"} {
		shared, err := manager.Get(alice, manager.MetadataConnectionScope(alice, logicalScope, "server"), "server")
		require.NoError(t, err)
		require.Same(t, gateway, shared)
	}
	require.EqualValues(t, 1, initializes.Load())
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	other, err := manager.Get(bob, manager.MetadataConnectionScope(bob, "discovery", "server"), "server")
	require.NoError(t, err)
	require.NotSame(t, gateway, other, "user isolation remains owned by the pool")
	execution, err := manager.Get(alice, "execution", "server")
	require.NoError(t, err)
	require.NotSame(t, gateway, execution, "tool execution retains its conversation transport")
	require.EqualValues(t, 3, initializes.Load())
}
