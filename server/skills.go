package server

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
)

func (h *Handler) ListSkills(ctx context.Context, request *jsonrpc.Request) (*schema.ListSkillsResult, *jsonrpc.Error) {
	handler, ok := h.handler.(protocol.Skills)
	if !ok {
		return nil, jsonrpc.NewMethodNotFound("skills/list is unavailable", nil)
	}
	params := &schema.ListSkillsRequest{Method: schema.MethodSkillsList}
	if err := unmarshalOptionalParams(request.Params, &params.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(err.Error(), nil)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return handler.ListSkills(ctx, &jsonrpc.TypedRequest[*schema.ListSkillsRequest]{Id: uint64(id), Method: request.Method, Request: params})
}

func (h *Handler) GetSkill(ctx context.Context, request *jsonrpc.Request) (*schema.GetSkillResult, *jsonrpc.Error) {
	handler, ok := h.handler.(protocol.Skills)
	if !ok {
		return nil, jsonrpc.NewMethodNotFound("skills/get is unavailable", nil)
	}
	params := &schema.GetSkillRequest{Method: schema.MethodSkillsGet}
	if err := json.Unmarshal(request.Params, &params.Params); err != nil {
		return nil, jsonrpc.NewInvalidParamsError(err.Error(), nil)
	}
	if params.Params.Uri == "" {
		return nil, jsonrpc.NewInvalidParamsError("skill URI is required", nil)
	}
	id, _ := jsonrpc.AsRequestIntId(request.Id)
	return handler.GetSkill(ctx, &jsonrpc.TypedRequest[*schema.GetSkillRequest]{Id: uint64(id), Method: request.Method, Request: params})
}
