// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"fmt"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
)

// SkillsClient is optional; older MCP client implementations remain usable.
type SkillsClient interface {
	ListSkills(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListSkillsResult, error)
	GetSkill(context.Context, string, ...mcpclient.RequestOption) (*schema.GetSkillResult, error)
}

func (c *managedClient) ListSkills(ctx context.Context, cursor *string, opts ...mcpclient.RequestOption) (*schema.ListSkillsResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	s, ok := client.(SkillsClient)
	if !ok {
		return nil, fmt.Errorf("MCP client does not support skills extension")
	}
	return s.ListSkills(ctx, cursor, opts...)
}
func (c *managedClient) GetSkill(ctx context.Context, uri string, opts ...mcpclient.RequestOption) (*schema.GetSkillResult, error) {
	client, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	s, ok := client.(SkillsClient)
	if !ok {
		return nil, fmt.Errorf("MCP client does not support skills extension")
	}
	return s.GetSkill(ctx, uri, opts...)
}
