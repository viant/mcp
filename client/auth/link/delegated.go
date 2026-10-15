package link

import "github.com/viant/mcp/client/auth/link/providerregistry"

type DelegatedMCPAuth struct {
	registry *providerregistry.Registry
	resolver *DelegatedCredentialResolver
}
