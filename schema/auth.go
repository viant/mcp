package schema

import (
	"github.com/viant/mcp/protocol/client/auth/meta"
	"reflect"
)

const (
	Unauthorized = -32001
)

type ( //these types are experimental and not part of the spec

	AuthConfig struct {
		Global *Authorization //represents the while MCP server as resource

		ExcludeURI string                    //this is experimental (not part of spec) exclude URI like /sse , in that case /message will be protected
		Tools      map[string]*Authorization //this is experimental (not part of spec)
		Tenants    map[string]*Authorization //this is experimental (not part of spec)
	}

	// WithAuth represents a request with authentication
	WithAuthMeta struct {
		Name     string   `json:"name"`
		AuthMeta AuthMeta `json:"_meta"`
	}

	// AuthMeta represents authentication metadata
	AuthMeta struct {
		Credentials *AuthToken `json:"authorization"`
	}

	// AuthToken represents authentication credentials
	AuthToken struct {
		Token string `json:"token"`
	}

	Authorization struct {
		ProtectedResourceMetadata *meta.ProtectedResourceMetadata `json:"protectedResourceMetadata"`
		RequiredScopes            []string                        `json:"requiredScopes"`
	}
)

var AuthTokenKey = reflect.TypeOf(AuthToken{})
