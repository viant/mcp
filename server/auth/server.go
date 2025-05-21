package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/server/http/session"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp-protocol/syncmap"
	"github.com/viant/mcp/client/auth/transport"
	"github.com/viant/scy/auth"
	"golang.org/x/oauth2"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	return s.Config.IsJSONRPCMediationMode() || (s.Policy.ExcludeURI != "" && strings.HasPrefix(r.URL.Path, s.Policy.ExcludeURI)) || r.Method != http.MethodPost
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
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		token := &authorization.Token{Token: authHeader}
		rWithToken := r.WithContext(context.WithValue(r.Context(), authorization.TokenKey, token))
		next.ServeHTTP(w, rWithToken)
		return
	}
	if s.FallBack != nil {
		if token, _ := s.FallBack.Token(r.Context(), rule); token != nil {
			rWithToken := r.WithContext(context.WithValue(r.Context(), authorization.TokenKey, token))
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

func (s *Service) getToken(r *http.Request, rule *authorization.Authorization, token *oauth2.Token) (*oauth2.Token, error) {
	if rule.UseIdToken {
		return auth.IdToken(r.Context(), token)
	}
	return token, nil
}

func (s *Service) getResourceKey(r *http.Request, rule *authorization.Authorization) string {
	sessionID := s.SessionIdProvider(r)
	resourceKey := sessionID + rule.ProtectedResourceMetadata.Resource
	return resourceKey
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
			sessionLocation := session.NewQueryLocation("session_id")
			streamingSessionLocation := session.NewQueryLocation("Mcp-Session-Id")
			if ret, _ := locator.Locate(sessionLocation, r); ret != "" {
				return ret
			}
			if ret, _ := locator.Locate(streamingSessionLocation, r); ret != "" {
				return ret
			}
			return ""
		},
	}, nil
}
