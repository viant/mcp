package transport

import (
	"context"
	"fmt"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"github.com/viant/mcp/client/auth/store"
	"github.com/viant/scy/auth/flow"
	"golang.org/x/oauth2"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type RoundTripper struct {
	Global          *authorization.Authorization
	store           store.Store
	scopper         Scopper
	useBFF          bool
	bffHeader       string
	authFlow        flow.AuthFlow
	bffFlow         flow.BackendForFrontendFlow
	authFlowOptions []flow.Option
	transport       http.RoundTripper
	jar             http.CookieJar
	ignoreCtxToken  bool
	rejected        map[string]time.Time
	rejectTTL       time.Duration
	mux             sync.Mutex
}

func (r *RoundTripper) Store() store.Store {
	return r.store
}

func (r *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// 1) First, send the request; if ctx carries an explicit token, attach it.
	probe := clone(req)
	// attach cookies from jar if present
	if r.jar != nil {
		for _, c := range r.jar.Cookies(probe.URL) {
			probe.AddCookie(c)
		}
	}
	var ctxToken string
	if !r.ignoreCtxToken {
		if token := getAuthToken(req.Context()); token != "" {
			if r.rejected != nil {
				if exp, ok := r.rejected[token]; !(ok && time.Now().Before(exp)) {
					ctxToken = token
				}
			} else {
				ctxToken = token
			}
		}
	}
	if ctxToken != "" {
		probe.Header.Set("Authorization", "Bearer "+ctxToken)
	}
	resp, err := r.transport.RoundTrip(probe)
	if err != nil {
		return nil, err
	}
	if r.jar != nil {
		r.jar.SetCookies(probe.URL, resp.Cookies())
	}

	// 2) If it wasn’t a 401, just return it.
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	// Close the prior body so we don’t leak.
	resp.Body.Close()

	ctx := req.Context()
	if ctxToken != "" {
		if r.rejected == nil {
			r.rejected = map[string]time.Time{}
		}
		ttl := r.rejectTTL
		if ttl <= 0 {
			ttl = 10 * time.Minute
		}
		r.rejected[ctxToken] = time.Now().Add(ttl)
	}
	if r.useBFF {
		if authorizationURI, _ := parseAuthorizationURI(resp); authorizationURI != "" {
			exchange, err := r.bffFlow.BeginAuthorization(req.Context(), authorizationURI)
			if err != nil {
				return nil, err
			}
			// 4) Replay the request with the exchange header.
			retry := clone(req)
			if r.jar != nil {
				for _, c := range r.jar.Cookies(retry.URL) {
					retry.AddCookie(c)
				}
			}
			retry.Header.Set(r.bffHeader, exchange.ToHeader())
			rresp, rerr := r.transport.RoundTrip(retry)
			if rerr == nil && r.jar != nil {
				r.jar.SetCookies(retry.URL, rresp.Cookies())
			}
			return rresp, rerr
		}
	}

	tok, err := r.Token(ctx, resp)
	if err != nil {
		return nil, err
	}

	if r.Global != nil && r.Global.UseIdToken {
		tok, err = r.IdToken(ctx, tok, r.Global.ProtectedResourceMetadata)
		if err != nil {
			return nil, err
		}
	}

	// 4) Replay the request with the Bearer header.
	retry := clone(req)
	if r.jar != nil {
		for _, c := range r.jar.Cookies(retry.URL) {
			retry.AddCookie(c)
		}
	}
	retry.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	rresp, rerr := r.transport.RoundTrip(retry)
	if rerr == nil && r.jar != nil {
		r.jar.SetCookies(retry.URL, rresp.Cookies())
	}
	return rresp, rerr
}

func (r *RoundTripper) Token(ctx context.Context, resp *http.Response) (*oauth2.Token, error) {
	r.mux.Lock()
	defer r.mux.Unlock()

	authServerMetadata, err := r.loadProtectedResourceMetadata(ctx, resp)
	if err != nil {
		return nil, err
	}
	scope := getScope(ctx)
	return r.ProtectedResourceToken(ctx, authServerMetadata, scope)
}

func (r *RoundTripper) ProtectedResourceToken(ctx context.Context, resourceMetadata *meta.ProtectedResourceMetadata, scope string) (*oauth2.Token, error) {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	authServers := resourceMetadata.AuthorizationServers
	issuer := authServers[rnd.Intn(len(authServers))]
	authorizationServerMetadata, _ := r.store.LookupAuthorizationServerMetadata(issuer)
	var err error
	if authorizationServerMetadata == nil {
		authorizationServerMetadata, err = meta.FetchAuthorizationServerMetadata(ctx, issuer, &http.Client{Transport: r.transport, Jar: r.jar})
		if err != nil {
			return nil, err
		}
		err = r.store.AddAuthorizationServerMetadata(authorizationServerMetadata)
		if err != nil {
			return nil, err
		}
	}

	//resourceMetadata.ScopesSupported

	tokenKey := store.TokenKey{authorizationServerMetadata.Issuer, scope}
	clientConfig, ok := r.store.LookupClientConfig(authorizationServerMetadata.Issuer)
	if !ok {
		return nil, fmt.Errorf("client config not found for issuer %s", authorizationServerMetadata.Issuer)
	}
	// if we have a cached token, return it or try refresh if expired
	if cached, _ := r.store.LookupToken(tokenKey); cached != nil {
		if cached.Valid() {
			return cached, nil
		}
		if cached.RefreshToken != "" {
			cached = r.refreshToken(ctx, clientConfig, cached)
			if cached != nil {
				if err := r.store.AddToken(tokenKey, cached); err != nil {
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

func (r *RoundTripper) refreshToken(ctx context.Context, clientConfig *oauth2.Config, cached *oauth2.Token) *oauth2.Token {
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

func (r *RoundTripper) loadProtectedResourceMetadata(ctx context.Context, resp *http.Response) (*meta.ProtectedResourceMetadata, error) {

	// ❷ Parse WWW‑Authenticate header.
	protectedResourceMetadataURL, err := parseAuthenticateHeader(resp)
	if err != nil {
		return nil, err
	}
	return meta.FetchProtectedResourceMetadata(ctx, protectedResourceMetadataURL, &http.Client{Transport: r.transport, Jar: r.jar})
}

func New(options ...Option) (*RoundTripper, error) {
	ret := &RoundTripper{
		transport: http.DefaultTransport,
		store:     store.NewMemoryStore(),
		authFlow:  &flow.BrowserFlow{},
		bffFlow:   &flow.BackendForFrontend{},
		scopper:   &nopScopper{},
		bffHeader: flow.AuthorizationExchangeHeader,
	}

	for _, opt := range options {
		opt(ret)
	}

	return ret, nil
}
