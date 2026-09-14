package client

import (
	"context"
	"github.com/viant/mcp-protocol/schema"
)

// ListSkills retrieves metadata only. It never prefetches skill content.
func (c *Client) ListSkills(ctx context.Context, cursor *string, options ...RequestOption) (*schema.ListSkillsResult, error) {
	return send[schema.ListSkillsRequestParams, schema.ListSkillsResult](ctx, c, schema.MethodSkillsList, &schema.ListSkillsRequestParams{Cursor: cursor}, options...)
}

func (c *Client) GetSkill(ctx context.Context, uri string, options ...RequestOption) (*schema.GetSkillResult, error) {
	return send[schema.GetSkillRequestParams, schema.GetSkillResult](ctx, c, schema.MethodSkillsGet, &schema.GetSkillRequestParams{Uri: uri}, options...)
}
