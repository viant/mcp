package transport

import (
	"context"
	"fmt"
	"net/url"

	"github.com/viant/scy/auth/flow"
	"github.com/viant/scy/auth/flow/endpoint"
	"golang.org/x/oauth2"
)

// AuthURLHandler is called with the OAuth authorization URL that the user
// must visit to complete authentication. The implementation should surface
// this URL to the user (e.g., via an MCP elicitation OOB event) and return
// immediately — the local callback server waits for the redirect.
type AuthURLHandler func(ctx context.Context, authURL string) error

// --- ElicitationFlow: replaces BrowserFlow (Token path) ---

// ElicitationFlow implements flow.AuthFlow by delegating the browser-open
// step to an AuthURLHandler callback. This allows web UIs to open the auth
// URL in a popup instead of trying to launch a CLI browser on the server.
type ElicitationFlow struct {
	Handler AuthURLHandler
}

func (f *ElicitationFlow) Token(ctx context.Context, config *oauth2.Config, options ...flow.Option) (*oauth2.Token, error) {
	if f.Handler == nil {
		return nil, fmt.Errorf("elicitation auth flow: no URL handler configured")
	}
	codeVerifier := flow.GenerateCodeVerifier()
	server, err := endpoint.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create callback server: %v", err)
	}
	go server.Start()

	redirectURL := fmt.Sprintf("http://localhost:%v/callback", server.Port)
	authURL, err := flow.BuildAuthCodeURL(config, append(options, flow.WithRedirectURI(redirectURL), flow.WithCodeVerifier(codeVerifier))...)
	if err != nil {
		return nil, err
	}

	if err := f.Handler(ctx, authURL); err != nil {
		return nil, fmt.Errorf("failed to surface auth URL: %v", err)
	}

	if err := server.Wait(); err != nil {
		return nil, fmt.Errorf("auth callback failed: %v", err)
	}
	code := server.AuthCode()
	if code == "" {
		return nil, fmt.Errorf("no auth code received")
	}
	return flow.Exchange(ctx, config, code, append(options, flow.WithCodeVerifier(codeVerifier), flow.WithRedirectURI(redirectURL))...)
}

// --- ElicitationBFFFlow: replaces BackendForFrontend (BFF path) ---

// ElicitationBFFFlow implements flow.BackendForFrontendFlow by delegating the
// browser-open step to an AuthURLHandler. Same as the default BFF flow but
// surfaces the authorization URL via callback instead of CLI browser.Open().
type ElicitationBFFFlow struct {
	Handler AuthURLHandler
}

func (f *ElicitationBFFFlow) BeginAuthorization(ctx context.Context, authorizationURI string) (*flow.AuthorizationExchange, error) {
	if f.Handler == nil {
		return nil, fmt.Errorf("elicitation BFF flow: no URL handler configured")
	}
	server, err := endpoint.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create callback server: %v", err)
	}
	go server.Start()

	result := &flow.AuthorizationExchange{}
	parsed, err := url.Parse(authorizationURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse authorizationURI: %v", err)
	}
	result.State = parsed.Query().Get("state")
	if result.State == "" {
		result.State = flow.GenerateCodeVerifier() // random token
		authorizationURI += "&state=" + result.State
	}
	result.RedirectURI = fmt.Sprintf("http://localhost:%v/callback", server.Port)
	authorizationURI += "&redirect_uri=" + url.QueryEscape(result.RedirectURI)

	// Surface the auth URL to the UI instead of opening a CLI browser.
	if err := f.Handler(ctx, authorizationURI); err != nil {
		return nil, fmt.Errorf("failed to surface BFF auth URL: %v", err)
	}

	if err := server.Wait(); err != nil {
		return nil, fmt.Errorf("BFF auth callback failed: %v", err)
	}
	result.Code = server.AuthCode()
	if result.Code == "" {
		return nil, fmt.Errorf("no auth code received from BFF flow")
	}
	return result, nil
}

// --- Options ---

// WithElicitationAuthFlow sets a custom AuthFlow that surfaces the OAuth
// authorization URL via a callback instead of opening a CLI browser.
func WithElicitationAuthFlow(handler AuthURLHandler) Option {
	return func(t *RoundTripper) {
		if handler != nil {
			t.authFlow = &ElicitationFlow{Handler: handler}
			t.bffFlow = &ElicitationBFFFlow{Handler: handler}
		}
	}
}
