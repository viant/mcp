package link

import "strings"

// Config configures the MCP authorization-code and delegated credential lifecycle.
// It does not configure application ingress, users, policies or login sessions.
type Config struct {
	OnCredentialChange         func(MCPAuthChangeEvent) `json:"-" yaml:"-"`
	WorkspaceNamespace         string                   `json:"workspaceNamespace,omitempty" yaml:"workspaceNamespace,omitempty"`
	TokenEncryptionKey         string                   `json:"tokenEncryptionKey,omitempty" yaml:"tokenEncryptionKey,omitempty"`
	StateEncryptionKey         string                   `json:"stateEncryptionKey,omitempty" yaml:"stateEncryptionKey,omitempty"`
	StateEncryptionKeyPrevious string                   `json:"stateEncryptionKeyPrevious,omitempty" yaml:"stateEncryptionKeyPrevious,omitempty"`
	MCPLinkStateTTLMinutes     int                      `json:"mcpLinkStateTTLMinutes,omitempty" yaml:"mcpLinkStateTTLMinutes,omitempty"`
	// OAuth preserves legacy configURL-derived key selection during migration.
	OAuth *OAuth `json:"oauth,omitempty" yaml:"oauth,omitempty"`
}
type OAuth struct {
	Name   string       `json:"name,omitempty" yaml:"name,omitempty"`
	Client *OAuthClient `json:"client,omitempty" yaml:"client,omitempty"`
}
type OAuthClient struct {
	Scopes         []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	WebUIScopes    []string `json:"webUIScopes,omitempty" yaml:"webUIScopes,omitempty"`
	MobileUIScopes []string `json:"mobileUIScopes,omitempty" yaml:"mobileUIScopes,omitempty"`
	CLIScopes      []string `json:"cliScopes,omitempty" yaml:"cliScopes,omitempty"`

	ConfigURL string `json:"configURL,omitempty" yaml:"configURL,omitempty"`
}

func (c *Config) DelegatedTokenEncryptionSalt() string {
	if c == nil {
		return ""
	}
	if key := strings.TrimSpace(c.TokenEncryptionKey); key != "" {
		return key
	}
	if c.OAuth != nil && c.OAuth.Client != nil {
		return strings.TrimSpace(c.OAuth.Client.ConfigURL)
	}
	return ""
}
