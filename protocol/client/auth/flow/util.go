package flow

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"golang.org/x/oauth2"
	"io"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func buildAuthCodeURL(redirectURL string, config *oauth2.Config, opts *Options) (string, error) {
	codeVerifier, err := opts.CodeVerifier()
	if err != nil {
		return "", err
	}
	codeChallenge := generateCodeChallenge(codeVerifier)
	var oauth2Options = []oauth2.AuthCodeOption{
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("redirect_uri", redirectURL),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	}
	for paramName, paramValue := range opts.authURLParams {
		oauth2Options = append(oauth2Options, oauth2.SetAuthURLParam(paramName, paramValue))
	}
	URL := config.AuthCodeURL(opts.State(), oauth2Options...)
	return URL, nil
}

// generateCodeChallenge creates a PKCE code challenge from a code verifier
func generateCodeChallenge(verifier string) string {
	sha := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sha[:])
}

// randomToken generates a cryptographically secure random token
func randomToken() string {
	const nBytes = 32

	buf := make([]byte, nBytes)

	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		// Fallback (should almost never happen).
		rnd := mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
		for i := range buf {
			buf[i] = byte(rnd.Intn(256))
		}
	}

	return base64.RawURLEncoding.EncodeToString(buf)
}

// postFormData  x-www-form-urlencoded POST
func postFormData(URL string, data map[string]string) (*http.Response, error) {
	form := url.Values{}
	for k, v := range data {
		form.Set(k, v)
	}
	req, err := http.NewRequest("POST", URL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// returning this prevents redirects
			return http.ErrUseLastResponse
		},
	}
	return client.Do(req)
}
