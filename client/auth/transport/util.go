package transport

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
)

func clone(r *http.Request) *http.Request {
	cloned := r.Clone(r.Context())
	// deep-copy body for idempotent POST replay
	if r.Body != nil {
		buf, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(buf))
		cloned.Body = io.NopCloser(bytes.NewBuffer(buf))
	}
	return cloned
}

func parseAuthorizationURI(resp *http.Response) (string, error) {
	authenticateHeader := resp.Header.Get("WWW-Authenticate")
	authenticateHeader = strings.TrimPrefix(authenticateHeader, "Bearer ") // ← n
	var URL string
	for _, part := range strings.Split(authenticateHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "authorization_uri=") {
			URL = strings.Trim(strings.TrimPrefix(part, "authorization_uri="), "\"")
			break
		}
	}
	if URL == "" {
		return "", errors.New("WWW-Authenticate missing authorization_uri param")
	}
	return URL, nil
}

func parseAuthenticateHeader(resp *http.Response) (string, error) {
	authenticateHeader := resp.Header.Get("WWW-Authenticate")
	authenticateHeader = strings.TrimPrefix(authenticateHeader, "Bearer ") // ← n
	var protectedResourceMetadataURL string
	for _, part := range strings.Split(authenticateHeader, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "resource_metadata=") {
			protectedResourceMetadataURL = strings.Trim(strings.TrimPrefix(part, "resource_metadata="), "\"")
			break
		}
	}
	if protectedResourceMetadataURL == "" {
		return "", errors.New("WWW-Authenticate missing resource_metadata param")
	}
	return protectedResourceMetadataURL, nil
}
