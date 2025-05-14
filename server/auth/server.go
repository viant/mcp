package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client/auth/transport"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// AuthServer acts as a broker between clients and external OAuth2/OIDC providers.
type AuthServer struct {
	Policy       *authorization.Policy
	RoundTripper *transport.RoundTripper
}

func NewAuthServer(policy *authorization.Policy) (*AuthServer, error) {
	return &AuthServer{Policy: policy}, nil
}

func MustNewAuthServer(policy *authorization.Policy) *AuthServer {
	s, err := NewAuthServer(policy)
	if err != nil {
		panic(err)
	}
	return s
}

func (s *AuthServer) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-protected-resource", s.protectedResourcesHandler)
}

func (s *AuthServer) Middleware(next http.Handler) http.Handler {
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

func (s *AuthServer) shouldBypass(r *http.Request) bool {
	return (s.Policy.ExcludeURI != "" && strings.HasPrefix(r.URL.Path, s.Policy.ExcludeURI)) || r.Method != http.MethodPost
}

func (s *AuthServer) extractJSONRPCRequest(r *http.Request) ([]byte, *jsonrpc.Request, error) {
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

func (s *AuthServer) resolveAuthorizationRule(jRequest *jsonrpc.Request) (*authorization.Authorization, string) {
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

func (s *AuthServer) handleAuthorization(w http.ResponseWriter, r *http.Request, next http.Handler, resourceURI string, rule *authorization.Authorization) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		token := &authorization.Token{Token: authHeader}
		rWithToken := r.WithContext(context.WithValue(r.Context(), authorization.TokenKey, token))
		next.ServeHTTP(w, rWithToken)
		return
	}
	resourceQuery := ""
	if resourceURI != "" {
		resourceQuery = fmt.Sprintf("?resource=%s", resourceURI)
	}

	proto, host := extractProtoAndHost(r)
	metaURL := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource%s", proto, host, resourceQuery)
	scopePart := ""
	if len(rule.RequiredScopes) > 0 {
		scopePart = fmt.Sprintf(`, scope="%s"`, strings.Join(rule.RequiredScopes, " "))
	}
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s%s"`, metaURL, scopePart))
	w.WriteHeader(http.StatusUnauthorized)
}

func (s *AuthServer) protectedResourcesHandler(w http.ResponseWriter, request *http.Request) {
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
