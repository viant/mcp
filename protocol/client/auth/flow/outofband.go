package flow

import (
	"context"
	"fmt"
	"golang.org/x/oauth2"
	"net/url"
	"strings"
)

type OutOfBandFlow struct{}

func (s *OutOfBandFlow) Token(ctx context.Context, config *oauth2.Config, options ...Option) (*oauth2.Token, error) {
	opts := NewOptions(options)
	redirectURL := "https://localhost/callback.html"
	URL, err := buildAuthCodeURL(redirectURL, config, opts)
	if err != nil {
		return nil, err
	}
	resp, err := postFormData(URL, opts.postParams)
	if err != nil {
		return nil, fmt.Errorf("failed to post form data %v", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}
	location := resp.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("missing location header")
	}
	parsedURL, err := url.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("failed to parse location %v", err)
	}
	errorMessage := parsedURL.Query().Get("error")
	if errorMessage != "" {
		return nil, fmt.Errorf(errorMessage)
	}
	code := parsedURL.Query().Get("code")
	if code == "" {
		return nil, fmt.Errorf("missing code in location %v", location)
	}
	tkn, err := config.Exchange(ctx, code,
		oauth2.SetAuthURLParam("scope", strings.Join(opts.scopes, ",")),
		oauth2.SetAuthURLParam("state", opts.State()),
		oauth2.SetAuthURLParam("grant_type", "authorization_code"),
		oauth2.SetAuthURLParam("code_verifier", opts.codeVerifier),
	)
	if tkn == nil && err == nil {
		err = fmt.Errorf("failed to get token")
	}
	return tkn, err
}

func NewOutOfBandFlow() *OutOfBandFlow {
	return &OutOfBandFlow{}
}
