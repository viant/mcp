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

func (s *Service) handleAuthCode(r *http.Request) (*oauth2.Token, error) {
	if s.Config.BackendForFrontend == nil {
		return nil, nil
	}
	headerValue := r.Header.Get(s.Config.BackendForFrontend.AuthCodeHeader)
	if headerValue != "" {

		code := extractFromHeader(headerValue, "code")
		if code == "" {
			code = extractFromHeader(headerValue, "auth_code")
		}
		if code == "" {
			code = headerValue
		}
		if sessionID := s.SessionIdProvider(r); sessionID != "" {
			cfg := s.Config.BackendForFrontend
			verifier, ok := s.codeVerifiers.Get(sessionID)
			if !ok {
				return nil, nil
			}
			s.codeVerifiers.Delete(sessionID)
			return flow.Exchange(r.Context(), cfg.Client, headerValue, flow.WithCodeVerifier(verifier.Code), flow.WithRedirectURI(cfg.RedirectURI))
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
	authorizationURI, _ := flow.BuildAuthCodeURL(cfg.Client, flow.WithPKCE(true), flow.WithState(state), flow.WithCodeVerifier(codeVerifier), flow.WithRedirectURI(cfg.RedirectURI))
	return authorizationURI
}
