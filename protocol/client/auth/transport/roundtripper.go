package transport

import (
	"context"
	"errors"
	"fmt"
	"github.com/viant/mcp/protocol/client/auth/client"
	flow2 "github.com/viant/mcp/protocol/client/auth/flow"
	meta2 "github.com/viant/mcp/protocol/client/auth/meta"
	"github.com/viant/mcp/protocol/client/auth/store"
	"golang.org/x/oauth2"
	"math/rand"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type RoundTripper struct {
	store           store.Store
	scopper         Scopper
	authFlow        flow2.AuthFlow
	authFlowOptions []flow2.Option
	transport       http.RoundTripper
	mux             sync.Mutex
}

func New(options ...Option) (*RoundTripper, error) {
	ret := &RoundTripper{
		transport: http.DefaultTransport,
		store:     store.NewMemoryStore(),
		authFlow:  &flow2.BrowserFlow{},
		scopper:   &nopScopper{},
	}

	for _, opt := range options {
		opt(ret)
	}

	return ret, nil
}

func (r *RoundTripper) Store() store.Store {
	return r.store
}

func (r *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1) First, send the request un-authenticated.
	probe := clone(req)
	resp, err := r.transport.RoundTrip(probe)
	if err != nil {
		return nil, err
	}

	// 2) If it wasn’t a 401, just return it.
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	// Close the prior body so we don’t leak.
	resp.Body.Close()

	// 3) Otherwise—we got a 401 → do the OAuth dance:
	ctx := context.WithValue(req.Context(), ContextRequestKey, req.URL)
	tok, err := r.Token(ctx)
	if err != nil {
		return nil, err
	}

	// 4) Replay the request with the Bearer header.
	retry := clone(req)
	retry.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	return r.transport.RoundTrip(retry)
}

func (r *RoundTripper) Token(ctx context.Context) (*oauth2.Token, error) {
	r.mux.Lock()
	defer r.mux.Unlock()

	reqURL, ok := ctx.Value(ContextRequestKey).(*url.URL)
	if !ok {
		return nil, errors.New("authServer discovery needs request URL in context")
	}
	scope := getScope(ctx)
	var err error
	authServerMetadata, err := r.discoverAuthorizationServer(ctx, reqURL)
	if err != nil {
		return nil, err
	}
	tokenKey := store.TokenKey{authServerMetadata.Issuer, scope}
	clientConfig, ok := r.store.LookupClientConfig(authServerMetadata.Issuer)
	if !ok {
		return nil, fmt.Errorf("client config not found for issuer %s", authServerMetadata.Issuer)
	}
	// if we have a cached token, return it or try refresh if expired
	if cached, _ := r.store.LookupToken(tokenKey); cached != nil {
		if cached.Valid() {
			return cached, nil
		}
		if cached.RefreshToken != "" {
			cached = r.refreshToken(ctx, clientConfig, cached)
			if cached != nil {
				if err = r.store.AddToken(tokenKey, cached); err != nil {
					return nil, fmt.Errorf("failed to store refreshed token: %v", err)
				}
				return cached, nil
			}
		}
	}

	authFlowOption := getAuthFlowOptions(ctx)
	// no valid or refreshed token; perform interactive auth
	token, err := r.authFlow.Token(ctx, (*oauth2.Config)(clientConfig), authFlowOption...)
	if err != nil {
		return nil, err
	}
	if serr := r.store.AddToken(tokenKey, token); serr != nil {
		return nil, fmt.Errorf("failed to store token: %v", serr)
	}
	return token, nil
}

func (r *RoundTripper) refreshToken(ctx context.Context, clientConfig *client.Config, cached *oauth2.Token) *oauth2.Token {
	cfg := (*oauth2.Config)(clientConfig)
	ts := cfg.TokenSource(ctx, cached)
	refreshed, err := ts.Token()
	if err == nil {
		// preserve refresh token if provider omitted it
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = cached.RefreshToken
		}
		return refreshed
	}
	return nil
}

func (r *RoundTripper) discoverAuthorizationServer(ctx context.Context, target *url.URL) (*meta2.AuthorizationServerMetadata, error) {
	// ❶ Send *exactly* the same request but without Authorization header.
	probe := &http.Request{
		Method: http.MethodGet, // use GET; HEAD may not trigger auth challenge
		URL:    target,
		Header: make(http.Header),
	}
	probe = probe.WithContext(ctx)
	resp, err := r.transport.RoundTrip(probe)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		return nil, errors.New("expected 401 for authServer discovery")
	}

	// ❷ Parse WWW‑Authenticate header.
	protectedResourceMetadataURL, err := parseAuthenticateHeader(resp)
	if err != nil {
		return nil, err
	}
	resourceMetadata, err := meta2.FetchProtectedResourceMetadata(ctx, protectedResourceMetadataURL, &http.Client{Transport: r.transport})
	if err != nil {
		return nil, err
	}

	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	authServers := resourceMetadata.AuthorizationServers
	issuer := authServers[rnd.Intn(len(authServers))]

	authorizationServerMetadata, _ := r.store.LookupAuthorizationServerMetadata(issuer)
	if authorizationServerMetadata != nil {
		return authorizationServerMetadata, nil
	}
	authorizationServerMetadata, err = meta2.FetchAuthorizationServerMetadata(ctx, issuer, &http.Client{Transport: r.transport})
	if err != nil {
		return nil, err
	}
	err = r.store.AddAuthorizationServerMetadata(authorizationServerMetadata)
	return authorizationServerMetadata, err
}
