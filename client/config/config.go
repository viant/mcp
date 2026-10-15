// SPDX-License-Identifier: Apache-2.0
// MCP client configuration extracted from Viant Agently Core.
package config

import (
	"fmt"
	"strings"

	mcp "github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"

	asynccfg "github.com/viant/mcp/client/config/async"
)

// Group is a simple list wrapper used by config with an optional URL root.
type Group[T any] struct {
	URL   string `yaml:"url,omitempty" json:"url,omitempty"`
	Items []T    `yaml:"items,omitempty" json:"items,omitempty"`
}

// MCPClient augments mcp.ClientOptions with optional discovery descriptions and metadata.
// ResolvedServiceIdentity is trusted reader provenance, never authored/wire data.
type ResolvedServiceIdentity struct {
	ServiceName      string
	ResourceURI      string
	ProviderIdentity string
}

type MCPClient struct {
	ResolvedIdentity  *ResolvedServiceIdentity `json:"-" yaml:"-"`
	ExecutionLocation string                   `json:"executionLocation,omitempty" yaml:"executionLocation,omitempty"`
	BrowserTransport  *BrowserTransport        `json:"browserTransport,omitempty" yaml:"browserTransport,omitempty"`
	BrowserTools      []string                 `json:"browserTools,omitempty" yaml:"browserTools,omitempty"`
	// PrimitiveNamespaces optionally narrows discovered provider namespaces.
	// An empty list accepts the namespaces authorized by the provider.
	PrimitiveNamespaces []string `yaml:"primitiveNamespaces,omitempty" json:"primitiveNamespaces,omitempty"`
	// PrimitiveProviderIdentity optionally pins a provider instance. Replacing
	// an observed instance on an existing connection requires this declaration.
	PrimitiveProviderIdentity string `yaml:"primitiveProviderIdentity,omitempty" json:"primitiveProviderIdentity,omitempty"`
	*mcp.ClientOptions        `yaml:",inline" json:",inline"`
	SkillDiscovery            *SkillDiscovery        `yaml:"skillDiscovery,omitempty" json:"skillDiscovery,omitempty"`
	Async                     []*asynccfg.Config     `yaml:"async,omitempty" json:"async,omitempty"`
	Descriptions              map[string]string      `yaml:"descriptions,omitempty" json:"descriptions,omitempty"`
	Metadata                  map[string]interface{} `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	// OutputArtifacts captures configured binary outputs before tool results
	// reach model context. Keys are exact, literal MCP tool names.
	OutputArtifacts map[string]OutputArtifact `yaml:"outputArtifacts,omitempty" json:"outputArtifacts,omitempty"`
	// Cacheable marks specific tools on this MCP server as eligible for
	// prompt-history supersession. Keys are tool names (exact match);
	// values indicate cacheability.
	Cacheable map[string]bool `yaml:"cacheable,omitempty" json:"cacheable,omitempty"`
	// ToolTimeoutSec overrides the default tool execution timeout when invoking
	// tools on this MCP server. When zero, a system default applies.
	ToolTimeoutSec int `yaml:"toolTimeoutSec,omitempty" json:"toolTimeoutSec,omitempty"`
	// DisableDelegatedAuth is the MCP-level delegated-auth kill switch. When
	// set on a server whose auth.mode is oauth, client creation fails with a
	// typed provider-disabled error; it never silently falls back to
	// workspace credentials.
	DisableDelegatedAuth bool `yaml:"disableDelegatedAuth,omitempty" json:"disableDelegatedAuth,omitempty"`
	// EagerLink asks an interactive Agently client to establish delegated OAuth
	// when this MCP server is first selected for use, before calling it remotely.
	// Discovery and workspace startup must not initiate the authorization flow.
	EagerLink bool `yaml:"eagerLink,omitempty" json:"eagerLink,omitempty"`
	// ToolsListVisibility declares whether tools/list is public workspace
	// metadata or private principal-scoped metadata. Remote servers default to
	// private when omitted. Public catalogs are warmed in the background and
	// reused from the shared registry; private catalogs are discovered lazily
	// and cached under the authenticated principal identity.
	ToolsListVisibility string `yaml:"toolsListVisibility,omitempty" json:"toolsListVisibility,omitempty"`
	// ResourceCacheScopes is explicit managing metadata classification by namespace.
	// Omitted namespaces remain user/credential/conversation scoped.
	ResourceCacheScopes map[string]string `yaml:"resourceCacheScopes,omitempty" json:"resourceCacheScopes,omitempty"`
	// ProtectedResourceMetadataURL pins a path-specific RFC 9728 document
	// when the origin-wide document describes a different MCP resource.
	ProtectedResourceMetadataURL string `yaml:"protectedResourceMetadataURL,omitempty" json:"protectedResourceMetadataURL,omitempty"`
}

const (
	ToolsListVisibilityPublic  = "public"
	ToolsListVisibilityPrivate = "private"
)

func (c *MCPClient) IsToolsListPublic() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.ToolsListVisibility), ToolsListVisibilityPublic)
}

// IsDelegatedAuth reports whether this MCP definition selects the delegated
// OAuth path (auth.mode=oauth with providerRef/inlineProvider).
func (c *MCPClient) IsDelegatedAuth() bool {
	if c == nil || c.ClientOptions == nil || c.ClientOptions.Auth == nil {
		return false
	}
	return c.ClientOptions.Auth.IsDelegated()
}

// NormalizeDelegatedAuth applies backward-compatible flag handling for
// delegated configs before viant/mcp requirement compilation: a legacy
// useIdToken=true with an empty tokenType normalizes to tokenType=idToken; an
// explicit conflicting tokenType (accessToken) combined with useIdToken=true
// fails validation. Non-delegated configs are untouched — legacy useIdToken
// behaviour stays unchanged for them.
func (c *MCPClient) NormalizeDelegatedAuth() error {
	if !c.IsDelegatedAuth() {
		return nil
	}
	auth := c.ClientOptions.Auth
	if !auth.UseIdToken {
		return nil
	}
	switch authcfg.TokenType(strings.TrimSpace(auth.TokenType)) {
	case "":
		auth.TokenType = string(authcfg.TokenTypeIDToken)
	case authcfg.TokenTypeIDToken:
		// Explicit and consistent.
	default:
		return fmt.Errorf("mcp auth: useIdToken=true conflicts with tokenType %q; drop useIdToken or set tokenType=%q", auth.TokenType, authcfg.TokenTypeIDToken)
	}
	return nil
}

const ResourceCachePublic = "public"
const ResourceCacheUser = "user"
const ResourceCacheConversation = "conversation"

func (c *MCPClient) ResourceCacheScope(namespace string, kind ...string) (string, error) {
	if c == nil {
		return ResourceCacheUser, nil
	}
	scope := c.ResourceCacheScopes[namespace]
	if len(kind) > 0 && c.ResourceCacheScopes[namespace+"/"+kind[0]] != "" {
		scope = c.ResourceCacheScopes[namespace+"/"+kind[0]]
	}
	if scope == "" {
		scope = c.ResourceCacheScopes["*"]
	}
	if scope == "" {
		return ResourceCacheUser, nil
	}
	if scope != ResourceCachePublic && scope != ResourceCacheUser && scope != ResourceCacheConversation {
		return "", fmt.Errorf("invalid resource metadata cache scope for namespace %q", namespace)
	}
	return scope, nil
}
