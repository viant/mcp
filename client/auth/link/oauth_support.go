package link

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/viant/mcp/client/auth/link/internal/authlog"
	scyauth "github.com/viant/scy/auth"
	"github.com/viant/scy/auth/authorizer"
	"github.com/viant/scy/kms"
	"github.com/viant/scy/kms/blowfish"
	"golang.org/x/oauth2"
)

type oauthInitiateResponse struct {
	AuthURL     string
	State       string
	RedirectURI string
}

type oauthStatePayload struct {
	CodeVerifier string   `json:"codeVerifier"`
	ReturnURL    string   `json:"returnURL,omitempty"`
	RedirectURI  string   `json:"redirectURI,omitempty"`
	Scopes       []string `json:"scopes,omitempty"`
}

type oauthScopeTarget string

const (
	oauthScopeTargetDefault oauthScopeTarget = ""
	oauthScopeTargetWebUI   oauthScopeTarget = "web"
	oauthScopeTargetMobile  oauthScopeTarget = "mobile"
	oauthScopeTargetCLI     oauthScopeTarget = "cli"
)

func callbackURL(r *http.Request, configuredPath string) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	path := strings.TrimSpace(configuredPath)
	if path == "" {
		path = "/v1/api/auth/oauth/callback"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return scheme + "://" + host + path
}

func identityFromOAuthToken(tok *oauth2.Token) (username, subject, email, idToken string) {
	if tok == nil {
		return "", "", "", ""
	}
	if raw := tok.Extra("id_token"); raw != nil {
		if s, ok := raw.(string); ok {
			idToken = strings.TrimSpace(s)
		}
	}
	return identityFromTokenStrings(idToken, tok.AccessToken)
}

func identityFromTokenStrings(idToken, accessToken string) (username, subject, email, rawID string) {
	rawID = strings.TrimSpace(idToken)
	claims := parseJWTClaims(rawID)
	if len(claims) == 0 {
		claims = parseJWTClaims(strings.TrimSpace(accessToken))
	}
	subject = claimString(claims, "sub")
	email = claimString(claims, "email")
	username = strings.TrimSpace(claimString(claims, "preferred_username"))
	if username == "" {
		username = strings.TrimSpace(claimString(claims, "name"))
	}
	if username == "" && email != "" {
		if idx := strings.Index(email, "@"); idx > 0 {
			username = email[:idx]
		} else {
			username = email
		}
	}
	if username == "" {
		username = subject
	}
	return username, subject, email, rawID
}

func parseJWTClaims(token string) map[string]interface{} {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]interface{}{}
	}
	seg := parts[1]
	switch len(seg) % 4 {
	case 2:
		seg += "=="
	case 3:
		seg += "="
	}
	data, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func bearerTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(header) < 8 || !strings.EqualFold(header[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(header[7:])
}

func claimString(claims map[string]interface{}, key string) string {
	raw, ok := claims[key]
	if !ok {
		return ""
	}
	val, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(val)
}

func claimUnixTime(claims map[string]interface{}, key string) (time.Time, bool) {
	raw, ok := claims[key]
	if !ok || raw == nil {
		return time.Time{}, false
	}
	switch actual := raw.(type) {
	case float64:
		return time.Unix(int64(actual), 0).UTC(), true
	case json.Number:
		if n, err := actual.Int64(); err == nil {
			return time.Unix(n, 0).UTC(), true
		}
	case int64:
		return time.Unix(actual, 0).UTC(), true
	case int:
		return time.Unix(int64(actual), 0).UTC(), true
	}
	return time.Time{}, false
}

func tokenExpiryFromTokenStrings(idToken, accessToken string) time.Time {
	for _, candidate := range []string{strings.TrimSpace(idToken), strings.TrimSpace(accessToken)} {
		if candidate == "" {
			continue
		}
		if exp, ok := claimUnixTime(parseJWTClaims(candidate), "exp"); ok {
			return exp
		}
	}
	return time.Time{}
}

func resolveTokenExpiry(explicitExpiresAt, idToken, accessToken string) time.Time {
	if expiry := strings.TrimSpace(explicitExpiresAt); expiry != "" {
		if parsed, err := time.Parse(time.RFC3339, expiry); err == nil {
			return parsed
		}
	}
	return tokenExpiryFromTokenStrings(idToken, accessToken)
}

func oauthScopesForTarget(client *OAuthClient, target oauthScopeTarget, explicit ...string) []string {
	if len(explicit) > 0 {
		if scopes := normalizeScopes(explicit); len(scopes) > 0 {
			return scopes
		}
	}
	if client != nil {
		baseScopes := normalizeScopes(client.Scopes)
		var surfaceScopes []string
		switch target {
		case oauthScopeTargetWebUI:
			surfaceScopes = normalizeScopes(client.WebUIScopes)
		case oauthScopeTargetMobile:
			surfaceScopes = normalizeScopes(client.MobileUIScopes)
		case oauthScopeTargetCLI:
			surfaceScopes = normalizeScopes(client.CLIScopes)
		}
		if len(surfaceScopes) > 0 {
			return normalizeScopes(append(append([]string(nil), baseScopes...), surfaceScopes...))
		}
		if len(baseScopes) > 0 {
			return baseScopes
		}
	}
	if target == oauthScopeTargetDefault {
		return []string{"openid"}
	}
	return []string{"openid", "profile", "email"}
}

// OAuthScopesForHeadless returns the grant a non-interactive user-owned
// runtime should request. A dedicated CLI scope wins; otherwise legacy
// browser-created schedules inherit the web scope, with mobile as the final
// compatibility fallback.
func OAuthScopesForHeadless(client *OAuthClient) []string {
	if client == nil {
		return []string{"openid"}
	}
	baseScopes := normalizeScopes(client.Scopes)
	surfaceScopes := normalizeScopes(client.CLIScopes)
	if len(surfaceScopes) == 0 {
		surfaceScopes = normalizeScopes(client.WebUIScopes)
	}
	if len(surfaceScopes) == 0 {
		surfaceScopes = normalizeScopes(client.MobileUIScopes)
	}
	scopes := normalizeScopes(append(append([]string(nil), baseScopes...), surfaceScopes...))
	if len(scopes) == 0 {
		return []string{"openid"}
	}
	return scopes
}

func normalizeScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return nil
	}
	result := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		result = append(result, scope)
	}
	return result
}

func configuredOAuthScopeSets(client *OAuthClient) [][]string {
	if client == nil {
		return nil
	}
	base := normalizeScopes(client.Scopes)
	surfaces := [][]string{
		normalizeScopes(client.WebUIScopes),
		normalizeScopes(client.MobileUIScopes),
		normalizeScopes(client.CLIScopes),
	}
	candidates := make([][]string, 0, len(surfaces))
	for _, surface := range surfaces {
		if len(surface) == 0 {
			continue
		}
		combined := append(append([]string(nil), base...), surface...)
		candidates = append(candidates, normalizeScopes(combined))
	}
	if len(candidates) == 0 && len(base) > 0 {
		candidates = append(candidates, base)
	}
	result := make([][]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if len(candidate) == 0 {
			continue
		}
		key := strings.Join(candidate, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, candidate)
	}
	return result
}

var oidcScopeSet = map[string]bool{
	"openid":         true,
	"profile":        true,
	"email":          true,
	"phone":          true,
	"address":        true,
	"offline_access": true,
}

func tokenScopesFromStrings(tokens ...string) []string {
	merged := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if scopes := tokenScopes(token); len(scopes) > 0 {
			merged = append(merged, scopes...)
		}
	}
	return normalizeScopes(merged)
}

func tokenScopes(token string) []string {
	claims := parseJWTClaims(token)
	if len(claims) == 0 {
		return nil
	}
	if scope := claimString(claims, "scope"); scope != "" {
		return normalizeScopes(strings.Fields(scope))
	}
	if raw, ok := claims["scp"]; ok {
		switch actual := raw.(type) {
		case string:
			return normalizeScopes(strings.Fields(actual))
		case []interface{}:
			values := make([]string, 0, len(actual))
			for _, item := range actual {
				if s, ok := item.(string); ok {
					values = append(values, s)
				}
			}
			return normalizeScopes(values)
		}
	}
	return nil
}

func tokenAudiences(token string) []string {
	claims := parseJWTClaims(token)
	if len(claims) == 0 {
		return nil
	}
	switch actual := claims["aud"].(type) {
	case string:
		if value := strings.TrimSpace(actual); value != "" {
			return []string{value}
		}
	case []interface{}:
		result := make([]string, 0, len(actual))
		for _, item := range actual {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				result = append(result, strings.TrimSpace(s))
			}
		}
		return result
	}
	return nil
}

func hasAllScopes(actual, required []string) bool {
	if len(required) == 0 {
		return true
	}
	if len(actual) == 0 {
		return false
	}
	set := map[string]bool{}
	for _, item := range normalizeScopes(actual) {
		set[item] = true
	}
	for _, item := range normalizeScopes(required) {
		if !set[item] {
			return false
		}
	}
	return true
}

func discriminatorScopes(scopes []string) []string {
	scopes = normalizeScopes(scopes)
	if len(scopes) == 0 {
		return nil
	}
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if oidcScopeSet[scope] {
			continue
		}
		result = append(result, scope)
	}
	return result
}

func validateConfiguredOAuthScopes(client *OAuthClient, expected []string, tokens ...string) error {
	expected = normalizeScopes(expected)
	return validateOAuthScopeSet(client, expected, tokenScopesFromStrings(tokens...))
}

func validateRefreshedOAuthScopes(client *OAuthClient, expected []string, token *oauth2.Token, idToken string) error {
	if token == nil {
		return fmt.Errorf("oauth refresh returned no token")
	}
	expected = normalizeScopes(expected)
	if responseScopes, present := oauthResponseScopes(token); present {
		return validateCompleteOAuthScopeSet(client, expected, responseScopes)
	}
	actual := tokenScopesFromStrings(idToken, token.AccessToken, token.RefreshToken)
	if len(actual) == 0 {
		actual = append([]string(nil), expected...)
	}
	return validateOAuthScopeSet(client, expected, actual)
}

func oauthResponseScopes(token *oauth2.Token) ([]string, bool) {
	if token == nil {
		return nil, false
	}
	raw := token.Extra("scope")
	switch actual := raw.(type) {
	case nil:
		return nil, false
	case string:
		actual = strings.TrimSpace(actual)
		if actual == "" {
			// oauth2.Token.Extra for url.Values uses Values.Get, collapsing omitted scope and scope=.
			// RFC 6749 §§5.1/6 retain requested scopes on omission; for compatibility,
			// explicit empty deliberately follows the same fallback.
			return nil, false
		}
		return normalizeScopes(strings.Fields(actual)), true
	case []string:
		return normalizeScopes(actual), true
	case []interface{}:
		scopes := make([]string, 0, len(actual))
		for _, item := range actual {
			if value, ok := item.(string); ok {
				scopes = append(scopes, value)
			}
		}
		return normalizeScopes(scopes), true
	default:
		return nil, true
	}
}

func validateCompleteOAuthScopeSet(client *OAuthClient, expected, tokenScopes []string) error {
	expected = normalizeScopes(expected)
	tokenScopes = normalizeScopes(tokenScopes)
	if len(expected) > 0 {
		if hasAllScopes(tokenScopes, expected) {
			return nil
		}
		return fmt.Errorf("oauth token is missing required scopes: need %s", strings.Join(expected, " "))
	}
	configured := configuredOAuthScopeSets(client)
	if len(configured) == 0 {
		return nil
	}
	for _, candidate := range configured {
		if hasAllScopes(tokenScopes, candidate) {
			return nil
		}
	}
	options := make([]string, 0, len(configured))
	for _, item := range configured {
		options = append(options, strings.Join(item, " "))
	}
	return fmt.Errorf("oauth token is missing configured scopes: need one of [%s]", strings.Join(options, "] ["))
}

// ValidateOAuthTokenScopes validates a newly obtained OAuth token against the
// exact scopes requested for that authorization flow.
func ValidateOAuthTokenScopes(expected []string, token *scyauth.Token) error {
	if token == nil {
		return fmt.Errorf("oauth authorization returned no token")
	}
	return validateRefreshedOAuthScopes(nil, expected, &token.Token, token.IDToken)
}

func validateOAuthScopeSet(client *OAuthClient, expected, tokenScopes []string) error {
	expected = normalizeScopes(expected)
	tokenScopes = normalizeScopes(tokenScopes)
	if len(expected) > 0 {
		if hasAllScopes(tokenScopes, expected) {
			return nil
		}
		return fmt.Errorf("oauth token is missing required scopes: need %s", strings.Join(expected, " "))
	}
	configured := configuredOAuthScopeSets(client)
	if len(configured) == 0 {
		return nil
	}
	for _, candidate := range configured {
		if hasAllScopes(tokenScopes, candidate) {
			return nil
		}
	}
	options := make([]string, 0, len(configured))
	for _, item := range configured {
		options = append(options, strings.Join(item, " "))
	}
	return fmt.Errorf("oauth token is missing configured scopes: need one of [%s]", strings.Join(options, "] ["))
}

func cloneOAuthConfigWithScopes(config *oauth2.Config, scopes []string) *oauth2.Config {
	if config == nil {
		return nil
	}
	clone := *config
	clone.Scopes = append([]string(nil), normalizeScopes(scopes)...)
	return &clone
}

func oauthRefreshScopes(sess *Session, token *OAuthToken) []string {
	if sess != nil {
		if scopes := normalizeScopes(sess.Scopes); len(scopes) > 0 {
			return scopes
		}
		if sess.Tokens != nil {
			if scopes := tokenScopesFromStrings(sess.Tokens.RefreshToken, sess.Tokens.IDToken, sess.Tokens.AccessToken); len(scopes) > 0 {
				return scopes
			}
		}
	}
	if token != nil {
		if scopes := tokenScopesFromStrings(token.RefreshToken, token.IDToken, token.AccessToken); len(scopes) > 0 {
			return scopes
		}
	}
	return nil
}

func oauthRefreshScopesForClient(sess *Session, token *OAuthToken, client *OAuthClient) []string {
	scopes := oauthRefreshScopes(sess, token)
	if len(discriminatorScopes(scopes)) > 0 {
		return scopes
	}
	return normalizeScopes(append(scopes, OAuthScopesForHeadless(client)...))
}

func oauthRefreshResource(sess *Session, token *OAuthToken, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	candidates := []string{}
	if sess != nil && sess.Tokens != nil {
		candidates = append(candidates, tokenAudiences(sess.Tokens.RefreshToken)...)
		candidates = append(candidates, tokenAudiences(sess.Tokens.AccessToken)...)
		candidates = append(candidates, tokenAudiences(sess.Tokens.IDToken)...)
	}
	if token != nil {
		candidates = append(candidates, tokenAudiences(token.RefreshToken)...)
		candidates = append(candidates, tokenAudiences(token.AccessToken)...)
		candidates = append(candidates, tokenAudiences(token.IDToken)...)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if clientID != "" && candidate == clientID {
			continue
		}
		return candidate
	}
	return ""
}

const maxOAuthTokenResponseBytes = 1 << 20

type oauthAuthStyleCacheKey struct {
	tokenURL string
	clientID string
}

var oauthRefreshAuthStyles sync.Map

type oauthRefreshStageError struct {
	stage  string
	status int
	err    error
}

func (e *oauthRefreshStageError) Error() string {
	if e == nil || e.err == nil {
		return "oauth refresh failed"
	}
	return "oauth refresh " + e.stage + ": " + e.err.Error()
}

func (e *oauthRefreshStageError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func refreshOAuthToken(ctx context.Context, config *oauth2.Config, base *oauth2.Token, scopes []string, resource string) (*oauth2.Token, error) {
	started := time.Now()
	if config == nil {
		err := &oauthRefreshStageError{stage: "config", err: fmt.Errorf("oauth2 config is nil")}
		logOAuthRefreshFailure(ctx, "", started, err)
		return nil, err
	}
	if base == nil {
		err := &oauthRefreshStageError{stage: "request", err: fmt.Errorf("oauth refresh token is nil")}
		logOAuthRefreshFailure(ctx, config.Endpoint.TokenURL, started, err)
		return nil, err
	}
	if strings.TrimSpace(base.RefreshToken) == "" {
		err := &oauthRefreshStageError{stage: "request", err: fmt.Errorf("oauth refresh token is empty")}
		logOAuthRefreshFailure(ctx, config.Endpoint.TokenURL, started, err)
		return nil, err
	}
	scopes = normalizeScopes(scopes)
	resource = strings.TrimSpace(resource)
	values := url.Values{}
	values.Set("grant_type", "refresh_token")
	values.Set("refresh_token", strings.TrimSpace(base.RefreshToken))
	if len(scopes) > 0 {
		values.Set("scope", strings.Join(scopes, " "))
	}
	if resource != "" {
		values.Set("resource", resource)
	}

	style := config.Endpoint.AuthStyle
	probe := style == oauth2.AuthStyleAutoDetect
	cacheKey := oauthAuthStyleCacheKey{
		tokenURL: strings.TrimSpace(config.Endpoint.TokenURL),
		clientID: strings.TrimSpace(config.ClientID),
	}
	if probe {
		if cached, ok := oauthRefreshAuthStyles.Load(cacheKey); ok {
			style = cached.(oauth2.AuthStyle)
			probe = false
		} else {
			style = oauth2.AuthStyleInHeader
		}
	}

	token, err := doOAuthRefreshRoundTrip(ctx, config, values, style)
	if err != nil && probe && shouldRetryOAuthClientAuth(err) {
		firstStatus, firstCode := oauthRefreshErrorDetails(err)
		token, err = doOAuthRefreshRoundTrip(ctx, config, values, oauth2.AuthStyleInParams)
		if err == nil {
			oauthRefreshAuthStyles.Store(cacheKey, oauth2.AuthStyleInParams)
			authlog.Log(ctx, authlog.Event{
				Op:             "refresh_client_auth_probe",
				Endpoint:       config.Endpoint.TokenURL,
				HTTPStatus:     firstStatus,
				OAuthErrorCode: firstCode,
				Classification: "client_auth_rejected",
				Action:         "retry_params",
				Recovered:      true,
				Elapsed:        time.Since(started),
			})
		}
	} else if err == nil && probe {
		oauthRefreshAuthStyles.Store(cacheKey, oauth2.AuthStyleInHeader)
	}
	if err != nil {
		logOAuthRefreshFailure(ctx, config.Endpoint.TokenURL, started, err)
		return nil, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = strings.TrimSpace(base.RefreshToken)
	}
	return token, nil
}

func doOAuthRefreshRoundTrip(ctx context.Context, config *oauth2.Config, values url.Values, style oauth2.AuthStyle) (*oauth2.Token, error) {
	requestValues := cloneURLValues(values)
	switch style {
	case oauth2.AuthStyleInParams:
		if config.ClientID != "" {
			requestValues.Set("client_id", config.ClientID)
		}
		if config.ClientSecret != "" {
			requestValues.Set("client_secret", config.ClientSecret)
		}
	case oauth2.AuthStyleInHeader:
	default:
		return nil, &oauthRefreshStageError{stage: "config", err: fmt.Errorf("unsupported oauth auth style %d", style)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.Endpoint.TokenURL, strings.NewReader(requestValues.Encode()))
	if err != nil {
		return nil, &oauthRefreshStageError{stage: "request", err: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if style == oauth2.AuthStyleInHeader {
		req.SetBasicAuth(url.QueryEscape(config.ClientID), url.QueryEscape(config.ClientSecret))
	}
	client := http.DefaultClient
	if httpClient, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && httpClient != nil {
		client = httpClient
	}
	resp, err := client.Do(req)
	if err != nil {
		stage := "transport"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			stage = "timeout"
		}
		return nil, &oauthRefreshStageError{stage: stage, err: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthTokenResponseBytes))
	if err != nil {
		return nil, &oauthRefreshStageError{stage: "response_read", status: resp.StatusCode, err: err}
	}
	return parseOAuthRefreshResponse(resp, body)
}

func parseOAuthRefreshResponse(resp *http.Response, body []byte) (*oauth2.Token, error) {
	retrieval := &oauth2.RetrieveError{Response: resp, Body: body}
	failedStatus := resp == nil || resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices
	contentType := ""
	if resp != nil {
		contentType, _, _ = mime.ParseMediaType(resp.Header.Get("Content-Type"))
	}
	trimmedBody := strings.TrimSpace(string(body))

	var (
		token *oauth2.Token
		err   error
	)
	if contentType == "application/x-www-form-urlencoded" || contentType == "text/plain" ||
		(contentType == "" && !strings.HasPrefix(trimmedBody, "{") && strings.Contains(trimmedBody, "=")) {
		token, err = parseOAuthFormResponse(trimmedBody, retrieval)
	} else {
		token, err = parseOAuthJSONResponse(body, retrieval)
	}
	if err != nil {
		if failedStatus && strings.TrimSpace(string(body)) == "" {
			return nil, retrieval
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, &oauthRefreshStageError{stage: "parse", status: status, err: err}
	}
	if failedStatus || strings.TrimSpace(retrieval.ErrorCode) != "" {
		return nil, retrieval
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, &oauthRefreshStageError{stage: "parse", err: fmt.Errorf("server response missing access_token")}
	}
	return token, nil
}

func parseOAuthJSONResponse(body []byte, retrieval *oauth2.RetrieveError) (*oauth2.Token, error) {
	payload := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	retrieval.ErrorCode = oauthString(payload["error"])
	retrieval.ErrorDescription = oauthString(payload["error_description"])
	retrieval.ErrorURI = oauthString(payload["error_uri"])
	expiresIn, err := oauthSeconds(payload["expires_in"])
	if err != nil {
		return nil, err
	}
	token := &oauth2.Token{
		AccessToken:  oauthString(payload["access_token"]),
		TokenType:    oauthString(payload["token_type"]),
		RefreshToken: oauthString(payload["refresh_token"]),
		ExpiresIn:    expiresIn,
	}
	if expiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return token.WithExtra(payload), nil
}

func parseOAuthFormResponse(body string, retrieval *oauth2.RetrieveError) (*oauth2.Token, error) {
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, err
	}
	retrieval.ErrorCode = strings.TrimSpace(values.Get("error"))
	retrieval.ErrorDescription = strings.TrimSpace(values.Get("error_description"))
	retrieval.ErrorURI = strings.TrimSpace(values.Get("error_uri"))
	expiresIn, err := oauthSeconds(values.Get("expires_in"))
	if err != nil {
		return nil, err
	}
	token := &oauth2.Token{
		AccessToken:  strings.TrimSpace(values.Get("access_token")),
		TokenType:    strings.TrimSpace(values.Get("token_type")),
		RefreshToken: strings.TrimSpace(values.Get("refresh_token")),
		ExpiresIn:    expiresIn,
	}
	if expiresIn > 0 {
		token.Expiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return token.WithExtra(values), nil
}

func oauthString(value any) string {
	switch actual := value.(type) {
	case string:
		return strings.TrimSpace(actual)
	case json.Number:
		return strings.TrimSpace(actual.String())
	default:
		return ""
	}
}

func oauthSeconds(value any) (int64, error) {
	switch actual := value.(type) {
	case nil:
		return 0, nil
	case string:
		if strings.TrimSpace(actual) == "" {
			return 0, nil
		}
		return strconv.ParseInt(strings.TrimSpace(actual), 10, 64)
	case json.Number:
		return actual.Int64()
	case float64:
		return int64(actual), nil
	case int64:
		return actual, nil
	default:
		return 0, fmt.Errorf("invalid expires_in")
	}
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

func shouldRetryOAuthClientAuth(err error) bool {
	var staged *oauthRefreshStageError
	if errors.As(err, &staged) {
		return false
	}
	var retrieval *oauth2.RetrieveError
	if !errors.As(err, &retrieval) {
		return false
	}
	if retrieval.Response != nil && retrieval.Response.StatusCode >= http.StatusInternalServerError {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(retrieval.ErrorCode)) {
	case "invalid_client", "unauthorized_client":
		return true
	case "":
		return retrieval.Response != nil &&
			(retrieval.Response.StatusCode == http.StatusBadRequest || retrieval.Response.StatusCode == http.StatusUnauthorized)
	default:
		return false
	}
}

func oauthRefreshErrorDetails(err error) (int, string) {
	var staged *oauthRefreshStageError
	if errors.As(err, &staged) && staged.status != 0 {
		return staged.status, ""
	}
	var retrieval *oauth2.RetrieveError
	if !errors.As(err, &retrieval) {
		return 0, ""
	}
	status := 0
	if retrieval.Response != nil {
		status = retrieval.Response.StatusCode
	}
	return status, strings.ToLower(strings.TrimSpace(retrieval.ErrorCode))
}

func oauthRefreshErrorClassification(err error) string {
	var staged *oauthRefreshStageError
	if errors.As(err, &staged) {
		return staged.stage
	}
	status, code := oauthRefreshErrorDetails(err)
	switch {
	case code == "invalid_grant":
		return "invalid_grant"
	case code == "invalid_client" || code == "unauthorized_client":
		return "client_auth_rejected"
	case code != "":
		return "oauth_error"
	case status >= http.StatusInternalServerError:
		return "server_error"
	case status == http.StatusBadRequest || status == http.StatusUnauthorized:
		return "http_client_rejected"
	case status >= http.StatusBadRequest:
		return "http_error"
	default:
		return "refresh_error"
	}
}

func logOAuthRefreshFailure(ctx context.Context, endpoint string, started time.Time, err error) {
	status, code := oauthRefreshErrorDetails(err)
	logErr := err
	var retrieval *oauth2.RetrieveError
	if errors.As(err, &retrieval) {
		// RetrieveError.Error includes the consumed response body for bare
		// responses. Status/code fields carry the useful diagnostics without
		// ever placing a token endpoint body in logs.
		logErr = nil
	}
	authlog.Log(ctx, authlog.Event{
		Op:             "refresh",
		Endpoint:       endpoint,
		HTTPStatus:     status,
		OAuthErrorCode: code,
		Classification: oauthRefreshErrorClassification(err),
		Action:         "failed",
		Elapsed:        time.Since(started),
		Err:            logErr,
	})
}

var stateCipher = blowfish.Cipher{}

func encryptState(ctx context.Context, salt, value string) (string, error) {
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(strings.TrimSpace(salt))))}
	encrypted, err := stateCipher.Encrypt(ctx, key, []byte(value))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base64.URLEncoding.EncodeToString(encrypted), "="), nil
}

func encryptOAuthState(ctx context.Context, salt string, payload oauthStatePayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return encryptState(ctx, salt, string(data))
}

func decryptState(ctx context.Context, salt, state string) (string, error) {
	raw := strings.TrimSpace(state)
	switch len(raw) % 4 {
	case 2:
		raw += "=="
	case 3:
		raw += "="
	}
	data, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(strings.TrimSpace(salt))))}
	decrypted, err := stateCipher.Decrypt(ctx, key, data)
	if err != nil {
		return "", err
	}
	return string(decrypted), nil
}

func decryptOAuthState(ctx context.Context, salt, state string) (oauthStatePayload, error) {
	raw, err := decryptState(ctx, salt, state)
	if err != nil {
		return oauthStatePayload{}, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return oauthStatePayload{}, fmt.Errorf("empty state payload")
	}
	if strings.HasPrefix(raw, "{") {
		var payload oauthStatePayload
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return oauthStatePayload{}, err
		}
		return payload, nil
	}
	return oauthStatePayload{CodeVerifier: raw}, nil
}

func loadOAuthClientConfig(ctx context.Context, configURL string) (result *oauth2.Config, resultErr error) {
	started := time.Now()
	defer func() {
		if resultErr == nil {
			return
		}
		authlog.Log(ctx, authlog.Event{
			Op:             "config_load",
			Classification: "config",
			Action:         "failed",
			Elapsed:        time.Since(started),
			Err:            resultErr,
		})
	}()
	oa := authorizer.New()
	oc := &authorizer.OAuthConfig{ConfigURL: configURL}
	if err := oa.EnsureConfig(ctx, oc); err == nil && oc.Config != nil {
		return oc.Config, nil
	}
	path := strings.TrimSpace(configURL)
	if strings.HasPrefix(path, "file://") {
		if u, err := url.Parse(path); err == nil {
			path = u.Path
		}
	}
	if strings.Contains(path, "://") && !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("unsupported oauth config location")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		AuthURL      string   `json:"authURL"`
		TokenURL     string   `json:"tokenURL"`
		ClientID     string   `json:"clientID"`
		ClientSecret string   `json:"clientSecret"`
		RedirectURL  string   `json:"redirectURL"`
		Scopes       []string `json:"scopes"`
		AuthStyle    string   `json:"authStyle"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw.AuthURL) == "" || strings.TrimSpace(raw.TokenURL) == "" || strings.TrimSpace(raw.ClientID) == "" {
		return nil, fmt.Errorf("oauth config requires authURL, tokenURL, and clientID")
	}
	authStyle, err := parseOAuthAuthStyle(raw.AuthStyle)
	if err != nil {
		return nil, err
	}
	return &oauth2.Config{
		ClientID:     strings.TrimSpace(raw.ClientID),
		ClientSecret: strings.TrimSpace(raw.ClientSecret),
		RedirectURL:  strings.TrimSpace(raw.RedirectURL),
		Scopes:       append([]string(nil), raw.Scopes...),
		Endpoint: oauth2.Endpoint{
			AuthURL:   strings.TrimSpace(raw.AuthURL),
			TokenURL:  strings.TrimSpace(raw.TokenURL),
			AuthStyle: authStyle,
		},
	}, nil
}

func parseOAuthAuthStyle(raw string) (oauth2.AuthStyle, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		return oauth2.AuthStyleAutoDetect, nil
	case "header":
		return oauth2.AuthStyleInHeader, nil
	case "params":
		return oauth2.AuthStyleInParams, nil
	default:
		return oauth2.AuthStyleAutoDetect, fmt.Errorf("unsupported oauth authStyle")
	}
}
