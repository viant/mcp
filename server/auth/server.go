package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/viant/jsonrpc"
	streamauth "github.com/viant/jsonrpc/transport/server/auth"
	"github.com/viant/jsonrpc/transport/server/http/session"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/syncmap"
	"github.com/viant/mcp/client/auth/transport"
	"github.com/viant/scy/auth"
	"golang.org/x/oauth2"
)

// Service acts as a broker between clients and external OAuth2/OIDC providers.
type Service struct {
	*Config
	RoundTripper      *transport.RoundTripper
	FallBack          *FallbackAuth
	SessionIdProvider func(r *http.Request) string
	//These are used by the backend-to-frontend flow
	codeVerifiers *syncmap.Map[string, *Verifier]
	resourceToken *syncmap.Map[string, *oauth2.Token]

	// bffGrantStore holds references to the jsonrpc BFF auth store (shared with transport
	// handlers) to allow setting an auth cookie when authorization has been established.
	bffGrantStore     streamauth.Store
	bffAuthCookieName string
}

func (s *Service) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.ProtectedResourcesHandler)
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.shouldBypass(r) {
			next.ServeHTTP(w, r)
			return
		}
		data, jRequest, err := s.extractJSONRPCRequest(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(data)))
		authRule, resourceURI := s.resolveAuthorizationRule(jRequest)
		if authRule == nil {
			next.ServeHTTP(w, r)
			return
		}

		s.handleAuthorization(w, r, next, resourceURI, authRule)
	})
}

func (s *Service) shouldBypass(r *http.Request) bool {
	bypass := s.Config.IsJSONRPCMediationMode() || (s.Policy.ExcludeURI != "" && strings.HasPrefix(r.URL.Path, s.Policy.ExcludeURI)) || r.Method != http.MethodPost
	return bypass
}

func (s *Service) extractJSONRPCRequest(r *http.Request) ([]byte, *jsonrpc.Request, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, nil, err
	}
	defer r.Body.Close()

	type jsonrpcRequest jsonrpc.Request
	jRequest := &jsonrpcRequest{}
	if err := json.Unmarshal(data, jRequest); err != nil {
		return nil, nil, err
	}
	return data, (*jsonrpc.Request)(jRequest), nil
}

func (s *Service) resolveAuthorizationRule(jRequest *jsonrpc.Request) (*authorization.Authorization, string) {
	switch jRequest.Method {
	case schema.MethodResourcesRead:
		params := &schema.ReadResourceRequestParams{}
		if err := json.Unmarshal(jRequest.Params, params); err == nil {
			if rule, ok := s.Policy.Resources[params.Uri]; ok {
				return rule, params.Uri
			}
		}
	case schema.MethodToolsCall:
		params := &schema.CallToolRequestParams{}
		if err := json.Unmarshal(jRequest.Params, params); err == nil {
			if rule, ok := s.Policy.Tools[params.Name]; ok {
				return rule, "tool/" + params.Name
			}
		}
	default:
		return nil, ""
	}
	return s.Policy.Global, ""
}

func (s *Service) handleAuthorization(w http.ResponseWriter, r *http.Request, next http.Handler, resourceURI string, rule *authorization.Authorization) {
	err := s.ensureResourceToken(r, rule)
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	cookieName := s.bffAuthCookieName
	if cookieName == "" {
		cookieName = defaultBFFAuthCookieName
	}
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		token := &authorization.Token{Token: authHeader}
		rWithToken := r.WithContext(context.WithValue(r.Context(), authorization.TokenKey, token))
		s.ensureBFFAuthCookie(w, r)
		next.ServeHTTP(w, rWithToken)
		return
	}
	if s.FallBack != nil {
		if token, _ := s.FallBack.Token(r.Context(), rule); token != nil {
			rWithToken := r.WithContext(context.WithValue(r.Context(), authorization.TokenKey, token))
			s.ensureBFFAuthCookie(w, r)
			next.ServeHTTP(w, rWithToken)
			return
		}
	}
	resourceQuery := ""
	if resourceURI != "" {
		resourceQuery = fmt.Sprintf("?resource=%s", resourceURI)
	}
	proto, host := extractProtoAndHost(r)
	metaURL := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource%s", proto, host, resourceQuery)
	scopeFragment := ""
	if len(rule.RequiredScopes) > 0 {
		scopeFragment = fmt.Sprintf(`, scope="%s"`, strings.Join(rule.RequiredScopes, " "))
	}
	btfFragment := ""

	statusCode := http.StatusUnauthorized
	if s.BackendForFrontend != nil {
		if authURI := s.generateAuthorizationURI(r.Context(), r); authURI != "" {
			scopeFragment = fmt.Sprintf(`, authorization_uri="%s"`, authURI)
		}
	}

	w.Header().Set("MCP-Protocol-Version", schema.LatestProtocolVersion)
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s%s%s"`, metaURL, scopeFragment, btfFragment))
	w.WriteHeader(statusCode)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&jsonrpc.Error{
			Code:    schema.Unauthorized,
			Message: "Unauthorized: protected resource requires authorization",
			Data:    []byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())),
		})
		return
	}
}

// ensureBFFAuthCookie mints or refreshes a BFF auth cookie when authorization
// is established and a shared grant store is available. The cookie contains an
// opaque grant id; tokens are never stored in cookies.
func (s *Service) ensureBFFAuthCookie(w http.ResponseWriter, r *http.Request) {
	if s.bffGrantStore == nil {
		if defaultBFFGrantStore == nil {
			defaultBFFGrantStore = streamauth.NewMemoryStore(30*time.Minute, 24*time.Hour, 2*time.Minute)
		}
		s.bffGrantStore = defaultBFFGrantStore
	}
	if r.Method != http.MethodPost {
		return
	}
	name := s.bffAuthCookieName
	if s.Config != nil && s.Config.BackendForFrontend != nil && strings.TrimSpace(s.Config.BackendForFrontend.AuthorizationExchangeHeader) != "" {
		// Name remains default unless overridden globally; header presence only indicates BFF mode
	}
	if name == "" {
		name = defaultBFFAuthCookieName
	}
	if ck, err := r.Cookie(name); err == nil && ck.Value != "" {
		// touch existing grant and refresh cookie
		_ = s.bffGrantStore.Touch(r.Context(), ck.Value, time.Now())
		http.SetCookie(w, &http.Cookie{Name: name, Value: ck.Value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		return
	}
	// mint new grant and set cookie
	g := streamauth.NewGrant("")
	_ = s.bffGrantStore.Put(r.Context(), g)
	http.SetCookie(w, &http.Cookie{Name: name, Value: g.ID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

var aDbg struct {
	once sync.Once
	v    bool
}

func (s *Service) getToken(r *http.Request, rule *authorization.Authorization, token *oauth2.Token) (*oauth2.Token, error) {
	if rule.UseIdToken {
		return auth.IdToken(r.Context(), token)
	}
	return token, nil
}

func (s *Service) getResourceKey(r *http.Request, rule *authorization.Authorization) string {
	// Prefer a stable BFF auth cookie id when present so tokens survive
	// across transport session reconnects. Fall back to current session id.
	key := s.SessionIdProvider(r)
	name := s.bffAuthCookieName
	if name == "" {
		name = defaultBFFAuthCookieName
	}
	if ck, err := r.Cookie(name); err == nil && ck != nil && ck.Value != "" {
		key = ck.Value
	}
	return key + rule.ProtectedResourceMetadata.Resource
}

// expireVerifiersIfNeeded expires verifiers if the size exceeds 1000
func (s *Service) expireVerifiersIfNeeded() {
	if s.codeVerifiers.Size() > 1000 {
		var expired []string
		s.codeVerifiers.Range(func(key string, value *Verifier) bool {
			if value.Created.Sub(time.Now()) > time.Minute {
				expired = append(expired, key)
			}
			return true
		})
		for _, key := range expired {
			s.codeVerifiers.Delete(key)
		}
	}
}

// ProtectedResourcesHandler provides metadata about protected resources.
func (s *Service) ProtectedResourcesHandler(w http.ResponseWriter, request *http.Request) {
	resource := request.URL.Query().Get("resource")
	policyRule := s.Policy.Global

	if resource != "" {
		if unescaped, err := url.QueryUnescape(resource); err == nil {
			resource = unescaped
		}
		if strings.HasPrefix(resource, "tool/") {
			if rule, ok := s.Policy.Tools[strings.TrimPrefix(resource, "tool/")]; ok && rule.ProtectedResourceMetadata != nil {
				policyRule = rule
			}
		} else if rule, ok := s.Policy.Resources[resource]; ok && rule.ProtectedResourceMetadata != nil {
			policyRule = rule
		}
	}
	metadata := policyRule.ProtectedResourceMetadata
	if metadata == nil {
		metadata = s.Policy.Global.ProtectedResourceMetadata
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

func New(config *Config) (*Service, error) {
	return &Service{Config: config,
		codeVerifiers: syncmap.NewMap[string, *Verifier](),
		resourceToken: syncmap.NewMap[string, *oauth2.Token](),
		SessionIdProvider: func(r *http.Request) string {
			locator := session.Locator{}
			// Prefer streamable header, then classic SSE query, then streamable query
			streamingHeaderLocation := session.NewHeaderLocation("Mcp-Session-Id")
			sessionLocation := session.NewQueryLocation("session_id")
			streamingSessionLocation := session.NewQueryLocation("Mcp-Session-Id")
			if ret, _ := locator.Locate(streamingHeaderLocation, r); ret != "" {
				return ret
			}
			if ret, _ := locator.Locate(sessionLocation, r); ret != "" {
				return ret
			}
			if ret, _ := locator.Locate(streamingSessionLocation, r); ret != "" {
				return ret
			}
			return ""
		},
		bffGrantStore:     defaultBFFGrantStore,
		bffAuthCookieName: defaultBFFAuthCookieName,
	}, nil
}
