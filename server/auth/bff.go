package auth

import (
	"context"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"
	"net/http"
	"time"
)

// Verifier is used to store the code verifier for the backend-to-frontend flow
type Verifier struct {
	Code    string
	Created time.Time
}

func (s *Service) handleAuthorizationExchange(r *http.Request) (*oauth2.Token, error) {
	if s.Config.BackendForFrontend == nil {
		return nil, nil
	}
	headerValue := r.Header.Get(s.Config.BackendForFrontend.AuthorizationExchangeHeader)
	if headerValue != "" {
		authorizationExchange := &flow.AuthorizationExchange{}
		authorizationExchange.FromHeader(headerValue)
		if sessionID := s.SessionIdProvider(r); sessionID != "" && authorizationExchange.Code != "" {
			cfg := s.Config.BackendForFrontend
			verifier, ok := s.codeVerifiers.Get(sessionID)
			if !ok {
				return nil, nil
			}
			s.codeVerifiers.Delete(sessionID)
			return flow.Exchange(r.Context(), cfg.Client, authorizationExchange.Code, flow.WithPKCE(true),
				flow.WithCodeVerifier(verifier.Code),
				flow.WithRedirectURI(authorizationExchange.RedirectURI))
		}
	}
	return nil, nil
}

// generateAuthorizationURI generates the authorization URI for the backend-to-frontend flow.
func (s *Service) generateAuthorizationURI(ctx context.Context, r *http.Request) string {
	cfg := s.Config.BackendForFrontend
	sessionID := s.SessionIdProvider(r)
	codeVerifier := flow.GenerateCodeVerifier()
	state := flow.GenerateCodeVerifier()
	s.expireVerifiersIfNeeded()
	s.codeVerifiers.Put(sessionID, &Verifier{Code: codeVerifier, Created: time.Now()})
	var flowOptions = []flow.Option{
		flow.WithPKCE(true), flow.WithState(state), flow.WithCodeVerifier(codeVerifier),
	}
	if cfg.RedirectURI != "" {
		flowOptions = append(flowOptions, flow.WithRedirectURI(cfg.RedirectURI))
	}
	authorizationURI, _ := flow.BuildAuthCodeURL(cfg.Client, flowOptions...)
	return authorizationURI
}
