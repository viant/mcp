package auth

import (
	"net/http"
	"strings"

	"github.com/viant/mcp-protocol/authorization"
	"golang.org/x/oauth2"
)

func (s *Service) ensureResourceToken(r *http.Request, rule *authorization.Authorization) (err error) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader != "" {
		return nil
	}
	// Build keys for cache lookup
	ckName := defaultBFFAuthCookieName
	if s.bffAuthCookieName != "" {
		ckName = s.bffAuthCookieName
	}
	ckVal := ""
	if ck, cerr := r.Cookie(ckName); cerr == nil && ck != nil {
		ckVal = ck.Value
	}
	sessionID := s.SessionIdProvider(r)
	resource := rule.ProtectedResourceMetadata.Resource
	cookieKey := ""
	if ckVal != "" {
		cookieKey = ckVal + resource
	}
	sessionKey := sessionID + resource
	resourceKey := s.getResourceKey(r, rule)
	var token *oauth2.Token

	// 1) Prefer cookie-key hit (do not return early; proceed to attach header if found)
	if cookieKey != "" {
		if value, ok := s.resourceToken.Get(cookieKey); ok && value != nil {
			token = value
		} else if value, ok := s.resourceToken.Get(sessionKey); ok && value != nil {
			s.resourceToken.Put(cookieKey, value)
			token = value
		}
	}
	// 2) Fallback to computed resourceKey (may be session-based on first call)
	if token == nil {
		if value, ok := s.resourceToken.Get(resourceKey); ok && value != nil { //try get from cache
			token = value
		}
	}
	if token == nil {
		token, err = s.handleAuthorizationExchange(r) //try to get from backend-to-frontend flow
		if token != nil {
			// Store under the computed key; bridging will copy to cookie-key on next call if needed
			s.resourceToken.Put(resourceKey, token)
		}
	}
	if err != nil || token == nil {
		return err
	}
	tkn, err := s.getToken(r, rule, token) //finally decide to use id token or access token
	if err != nil {
		return err
	}
	//TODO check if token is expired and refresh
	r.Header.Set("Authorization", "Bearer "+tkn.AccessToken)
	return nil
}
