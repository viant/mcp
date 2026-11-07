// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package manager

import (
	"context"

	mcpcfg "github.com/viant/mcp/client/config"
)

type authTokenProviderConfigStub struct {
	cfg *mcpcfg.MCPClient
}

func (s *authTokenProviderConfigStub) Options(ctx context.Context, serverName string) (*mcpcfg.MCPClient, error) {
	return s.cfg, nil
}
