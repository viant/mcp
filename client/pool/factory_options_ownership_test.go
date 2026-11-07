package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	schema "github.com/viant/mcp-protocol/schema"
	authctx "github.com/viant/mcp/client/auth/link"
	authstore "github.com/viant/mcp/client/auth/store"
	authtransport "github.com/viant/mcp/client/auth/transport"
	cfg "github.com/viant/mcp/client/config"
)

type factoryObservedTransport struct{ calls atomic.Int32 }

func (t *factoryObservedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls.Add(1)
	return http.DefaultTransport.RoundTrip(r)
}

// These independent HTTP fixtures preserve the SDK's documented in-place
// negotiation while proving that the manager owns its per-client SDK options.
func TestFactoryPreservesProviderOptionsDuringAutomaticNegotiation(t *testing.T) {
	for _, mode := range []string{"plain", "cookie jar", "injected transport", "manager auth"} {
		t.Run(mode, func(t *testing.T) {
			var initializes atomic.Int32
			var sawCookie atomic.Bool
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if request.Method != "initialize" {
					w.WriteHeader(http.StatusAccepted)
					return
				}
				if cookie, err := r.Cookie("synthetic-route"); err == nil && cookie.Value == "enabled" {
					sawCookie.Store(true)
				}
				id := initializes.Add(1)
				w.Header().Set("Mcp-Session-Id", fmt.Sprint(id))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"protocolVersion": schema.LegacyProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fixture", "version": "1"}}})
			}))
			defer peer.Close()
			settings := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: peer.URL}}}}
			var options []Option
			var observed *factoryObservedTransport
			var originalStore authstore.Store
			if mode != "plain" {
				jar, err := cookiejar.New(nil)
				require.NoError(t, err)
				origin, err := url.Parse(peer.URL)
				require.NoError(t, err)
				jar.SetCookies(origin, []*http.Cookie{{Name: "synthetic-route", Value: "enabled"}})
				settings.CookieJar = jar
				if mode == "injected transport" || mode == "manager auth" {
					observed = &factoryObservedTransport{}
					rt, err := authtransport.New(authtransport.WithTransport(observed))
					require.NoError(t, err)
					originalStore = authstore.NewMemoryStore()
					settings.Auth = &mcp.ClientAuth{Store: originalStore}
					if mode == "injected transport" {
						settings.SetAuthTransport(rt, &http.Client{Transport: observed, Jar: jar})
					} else {
						options = append(options, WithAuthRoundTripper(rt), WithCookieJar(jar))
					}
				}
			}
			before, err := json.Marshal(settings)
			require.NoError(t, err)
			fingerprint, err := effectiveConfigFingerprint(settings)
			require.NoError(t, err)
			mgr, err := New(&catalogProvider{cfg: settings}, options...)
			require.NoError(t, err)
			defer mgr.CloseConversation("chat")
			alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			first, err := mgr.Get(alice, "chat", "fixture")
			require.NoError(t, err)
			negotiated, err := first.(interface {
				NegotiatedServer() (*schema.InitializeResult, error)
			}).NegotiatedServer()
			require.NoError(t, err)
			require.Equal(t, schema.LegacyProtocolVersion, negotiated.ProtocolVersion)
			after, err := json.Marshal(settings)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after), "runtime negotiation/injection must not mutate the provider's definition")
			require.Empty(t, settings.ProtocolVersion, "automatic negotiation remains authored as automatic")
			current, err := effectiveConfigFingerprint(settings)
			require.NoError(t, err)
			require.Equal(t, fingerprint, current)
			again, err := mgr.Get(alice, "chat", "fixture")
			require.NoError(t, err)
			require.Same(t, first, again)
			require.EqualValues(t, 1, initializes.Load(), "an unchanged definition keeps its valid cached client")
			if mode != "plain" {
				require.True(t, sawCookie.Load(), "opaque cookie jar state must reach the SDK")
			}
			if originalStore != nil {
				require.Same(t, originalStore, settings.Auth.Store)
				require.Positive(t, observed.calls.Load(), "the existing runtime transport must remain wired")
			}
		})
	}
}

func TestFactorySharedAutomaticOptionsRemainImmutableAcrossUsers(t *testing.T) {
	peer, _ := poolConfigEndpoint(t, "synthetic", nil, nil)
	settings := &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{Transport: mcp.ClientTransport{Type: "streamable", ClientTransportHTTP: mcp.ClientTransportHTTP{URL: peer.URL + "/mcp"}}}}
	before, err := json.Marshal(settings)
	require.NoError(t, err)
	mgr, err := New(&catalogProvider{cfg: settings})
	require.NoError(t, err)
	defer mgr.CloseConversation("chat")
	var group sync.WaitGroup
	failures := make(chan error, 4)
	for _, user := range []string{"alice", "bob", "carol", "dave"} {
		group.Add(1)
		go func() {
			defer group.Done()
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: user})
			_, err := mgr.Get(ctx, "chat", "fixture")
			failures <- err
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	after, err := json.Marshal(settings)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}
