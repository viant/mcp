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

	"github.com/stretchr/testify/assert"
	authcfg "github.com/viant/mcp/client/auth/config"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

// statefulResolver is a CredentialResolver test double whose Refresh rotates
// the current token.
type statefulResolver struct {
	mu           sync.Mutex
	current      string
	refreshed    string
	resolveCount int
	refreshCount int
}

func (s *statefulResolver) Resolve(context.Context, authcfg.Requirement) (*authcfg.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveCount++
	return &authcfg.Credential{Token: s.current, TokenType: authcfg.TokenTypeAccessToken}, nil
}

func (s *statefulResolver) Refresh(context.Context, authcfg.Requirement) (*authcfg.Credential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshCount++
	if s.refreshed != "" {
		s.current = s.refreshed
	}
	return &authcfg.Credential{Token: s.current, TokenType: authcfg.TokenTypeAccessToken}, nil
}

func (s *statefulResolver) Invalidate(context.Context, authcfg.Requirement) error { return nil }

func (s *statefulResolver) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveCount, s.refreshCount
}

type delegatedServerLog struct {
	mu             sync.Mutex
	initializeAuth []string
	rejected       int
}

// newDelegatedMCPServer serves the 2025-06-18 initialize handshake, accepting
// only the supplied bearer token and rejecting everything else with 401.
func newDelegatedMCPServer(t *testing.T, accepted string, log *delegatedServerLog) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		authorization := r.Header.Get("Authorization")
		if request.Method == "initialize" {
			log.mu.Lock()
			log.initializeAuth = append(log.initializeAuth, authorization)
			log.mu.Unlock()
		}
		if authorization != "Bearer "+accepted {
			log.mu.Lock()
			log.rejected++
			log.mu.Unlock()
			w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Mcp-Session-Id", "delegated-session")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"result": map[string]interface{}{
					"protocolVersion": "2025-06-18",
					"capabilities":    map[string]interface{}{},
					"serverInfo":      map[string]interface{}{"name": "delegated-server", "version": "1.0"},
				},
			})
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	}))
}

func newDelegatedClientOptions(serverURL string, resolver authcfg.CredentialResolver) *ClientOptions {
	return &ClientOptions{
		Name:            "viant-mcp-dev6",
		Version:         "1.0",
		ProtocolVersion: "2025-06-18",
		Transport: ClientTransport{
			Type:                "streamable",
			ClientTransportHTTP: ClientTransportHTTP{URL: serverURL},
		},
		Auth: &ClientAuth{
			Mode:                     "oauth",
			ProviderRef:              "adelphic-dev6",
			ClientRef:                "steward-web",
			Scopes:                   []string{"plan:create", "plan:edit", "plan:read"},
			WorkspaceTokenReuse:      "ifCompatible",
			AllowCrossOriginResource: true,
			ExternalResolver:         resolver,
		},
	}
}

// TestDelegatedInitialize_EagerResolutionAvoidsChallenge asserts that with an
// installed external resolver the credential is resolved before initialize —
// the server never observes an unauthenticated request or issues a 401.
func TestDelegatedInitialize_EagerResolutionAvoidsChallenge(t *testing.T) {
	log := &delegatedServerLog{}
	server := newDelegatedMCPServer(t, "good", log)
	defer server.Close()

	resolver := &statefulResolver{current: "good"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, newDelegatedClientOptions(server.URL, resolver))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resolveCount, refreshCount := resolver.counts()
	assert.GreaterOrEqual(t, resolveCount, 1, "credential resolved proactively before initialize")
	assert.Equal(t, 0, refreshCount)
	assert.Equal(t, 0, log.rejected, "no 401 challenge with eager resolution")
	assert.Equal(t, []string{"Bearer good"}, log.initializeAuth)
}

// TestDelegatedInitialize_SingleRefreshRetryOn401 asserts that a stale
// credential triggers exactly one refresh and one retry during initialize.
func TestDelegatedInitialize_SingleRefreshRetryOn401(t *testing.T) {
	log := &delegatedServerLog{}
	server := newDelegatedMCPServer(t, "good", log)
	defer server.Close()

	resolver := &statefulResolver{current: "stale", refreshed: "good"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, newDelegatedClientOptions(server.URL, resolver))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, refreshCount := resolver.counts()
	assert.Equal(t, 1, refreshCount, "exactly one refresh")
	assert.Equal(t, []string{"Bearer stale", "Bearer good"}, log.initializeAuth, "one initial attempt, one retry")
}

// TestDelegatedInitialize_LinkRequiredSurfaced asserts that when refresh does
// not produce an accepted credential, initialize fails with the typed
// link-required signal after exactly one refresh and one retry.
func TestDelegatedInitialize_LinkRequiredSurfaced(t *testing.T) {
	log := &delegatedServerLog{}
	server := newDelegatedMCPServer(t, "good", log)
	defer server.Close()

	resolver := &statefulResolver{current: "stale", refreshed: "still-bad"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := NewClientWithContext(ctx, &julyProtocolClientHandler{}, newDelegatedClientOptions(server.URL, resolver))
	if client != nil {
		defer client.Close()
	}
	assert.Error(t, err)
	var linkRequired *authcfg.OAuthLinkRequiredError
	assert.True(t, errors.As(err, &linkRequired), "expected typed link-required signal, got %T: %v", err, err)
	_, refreshCount := resolver.counts()
	assert.Equal(t, 1, refreshCount, "exactly one refresh, never a storm")
	assert.Equal(t, []string{"Bearer stale", "Bearer still-bad"}, log.initializeAuth, "exactly one retry")
}

// TestDelegatedClient_TransportOwnsAuthInterceptor asserts that the legacy
// JSON-RPC auth interceptor (which can start interactive flows) is not
// installed for delegated clients, while legacy clients keep it.
func TestDelegatedClient_TransportOwnsAuthInterceptor(t *testing.T) {
	delegated, err := authtransport.New(authtransport.WithCredentialResolver(&statefulResolver{current: "x"}, &authcfg.Requirement{ServerName: "dev6"}))
	assert.NoError(t, err)
	options := &ClientOptions{Name: "dev6"}
	clientOptions := options.Options(delegated)
	legacyRT, err := authtransport.New()
	assert.NoError(t, err)
	legacyOptions := options.Options(legacyRT)
	assert.Equal(t, len(clientOptions)+1, len(legacyOptions),
		"legacy transport installs the auth interceptor; delegated transport must not")
}
