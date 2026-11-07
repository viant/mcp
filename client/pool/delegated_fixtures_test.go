// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package manager

import (
	"context"

	"github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"
	mcpcfg "github.com/viant/mcp/client/config"
)

func delegatedConfig(disableSwitch bool) *mcpcfg.MCPClient {
	return &mcpcfg.MCPClient{
		DisableDelegatedAuth: disableSwitch,
		ClientOptions: &mcp.ClientOptions{
			Name: "viant-mcp-dev6",
			Auth: &mcp.ClientAuth{
				Mode:        authcfg.ModeOAuth,
				ProviderRef: "test-provider",
				ClientRef:   "example-web",
				Resource:    "https://mcp6.example.com/mcp",
				Scopes:      []string{"plan:create"},
			},
			Transport: mcp.ClientTransport{
				Type:                "streamable",
				ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "https://mcp6.example.com/mcp"},
			},
		},
	}
}

type staticCredentialResolver struct{}

func (staticCredentialResolver) Resolve(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: "delegated-token"}, nil
}
func (staticCredentialResolver) Refresh(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	return &authcfg.Credential{Token: "delegated-token"}, nil
}
func (staticCredentialResolver) Invalidate(ctx context.Context, requirement authcfg.Requirement) error {
	return nil
}
