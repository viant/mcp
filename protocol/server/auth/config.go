package auth

import (
	"github.com/viant/mcp/protocol/client/auth/meta"
)

type Config struct {
	Global     *meta.ProtectedResourceMetadata            //represents the while MCP server as resource
	ExcludeURI string                                     //this is experimental (not part of spec) exclude URI like /sse , in that case /message will be protected
	Tools      map[string]*meta.ProtectedResourceMetadata //this is experimental (not part of spec)
	Tenants    map[string]*meta.ProtectedResourceMetadata //this is experimental (not part of spec)
}
