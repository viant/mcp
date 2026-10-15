// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

var runtimeWorkerID = func() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}()

func refreshedOAuthIDToken(refreshed *oauth2.Token, current string) string {
	if refreshed == nil {
		current = strings.TrimSpace(current)
		if oauthJWTExpired(current, time.Now()) {
			return ""
		}
		return current
	}
	if raw := refreshed.Extra("id_token"); raw != nil {
		if token, ok := raw.(string); ok && strings.TrimSpace(token) != "" {
			token = strings.TrimSpace(token)
			if !oauthJWTExpired(token, time.Now()) {
				return token
			}
			return ""
		}
	}
	current = strings.TrimSpace(current)
	if oauthJWTExpired(current, time.Now()) {
		return ""
	}
	return current
}

func oauthJWTExpired(token string, now time.Time) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	expiresAt, ok := claimUnixTime(parseJWTClaims(token), "exp")
	return ok && !expiresAt.After(now.Add(30*time.Second))
}

// oauthJWTExpiry returns the exp claim of a JWT-shaped token; zero when the
// token is empty or carries no parseable exp.
func oauthJWTExpiry(token string) time.Time {
	if strings.TrimSpace(token) == "" {
		return time.Time{}
	}
	if expiresAt, ok := claimUnixTime(parseJWTClaims(token), "exp"); ok {
		return expiresAt
	}
	return time.Time{}
}

// isPermanentRefreshError reports whether a parsed token-endpoint response
// proves that the refresh credential itself is no longer usable. Only
// invalid_grant has that meaning; all other failures preserve credentials.
func isPermanentRefreshError(err error) bool {
	if err == nil {
		return false
	}
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(re.ErrorCode), "invalid_grant") {
		return false
	}
	return re.Response == nil || re.Response.StatusCode < http.StatusInternalServerError
}
