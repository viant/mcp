package client

import (
	"golang.org/x/oauth2"
	"strings"
)

// Config represents IAM GCP client
type Config oauth2.Config

// UseIdToken returns true if the client is configured to use id_token
func (c *Config) UseIdToken() bool {
	for _, scope := range c.Scopes {
		if strings.ToLower(scope) == "openid" {
			return true
		}
	}
	return false
}

// NewConfig creates a new OAuth client
func NewConfig(id, secret string, endpoint oauth2.Endpoint, scopes ...string) *Config {
	return &Config{
		ClientID:     id,
		ClientSecret: secret,
		Endpoint:     endpoint,
		Scopes:       scopes,
	}
}
