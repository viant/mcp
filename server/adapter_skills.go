package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client"
)

// Native skills use the same request sequencing and handler dispatch as other
// in-process standard methods. Authentication remains in the request context.
func (a *Adapter) ListSkills(ctx context.Context, cursor *string, options ...client.RequestOption) (*schema.ListSkillsResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodSkillsList, &schema.ListSkillsRequestParams{Cursor: cursor})
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
	}
	a.ensureRequestID(req)
	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)
	if response.Error != nil {
		return nil, response.Error
	}
	var result schema.ListSkillsResult
	if err = json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (a *Adapter) GetSkill(ctx context.Context, uri string, options ...client.RequestOption) (*schema.GetSkillResult, error) {
	req, err := jsonrpc.NewRequest(schema.MethodSkillsGet, &schema.GetSkillRequestParams{Uri: uri})
	if err != nil {
		return nil, err
	}
	if ro := client.NewRequestOptions(options); ro != nil && ro.RequestId != nil {
		req.Id = ro.RequestId
	}
	a.ensureRequestID(req)
	response := &jsonrpc.Response{}
	a.handler.Serve(ctx, req, response)
	if response.Error != nil {
		return nil, response.Error
	}
	var result schema.GetSkillResult
	if err = json.Unmarshal(response.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
