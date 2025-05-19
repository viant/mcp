package auth

import (
	"github.com/viant/mcp-protocol/authorization"
	"golang.org/x/oauth2"
	"net/http"
	"strings"
)

func (s *Service) ensureResourceToken(r *http.Request, rule *authorization.Authorization) (err error) {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader != "" {
		return nil
	}
	resourceKey := s.getResourceKey(r, rule)
	var token *oauth2.Token

	if value, ok := s.resourceToken.Get(resourceKey); ok { //try get from cache
		token = value
	}
	if token == nil {
		token, err = s.handleAuthCode(r) //try to get from backend-to-frontend flow
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
