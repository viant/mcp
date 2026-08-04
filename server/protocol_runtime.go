package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

const completeResultType = "complete"

// prepareProtocolRequest validates July metadata and places an immutable copy
// on this request's context. Legacy state is consulted only for legacy calls.
func (h *Handler) prepareProtocolRequest(ctx context.Context, request *jsonrpc.Request) (context.Context, string, *jsonrpc.Error) {
	if request.Method == schema.MethodInitialize {
		return ctx, schema.LegacyProtocolVersion, nil
	}
	var params map[string]interface{}
	if len(request.Params) > 0 && string(request.Params) != "null" {
		if err := json.Unmarshal(request.Params, &params); err != nil {
			return ctx, "", jsonrpc.NewInvalidParamsError(err.Error(), request.Params)
		}
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	meta, _ := params["_meta"].(map[string]interface{})
	if meta == nil {
		legacyVersion := h.legacyProtocolVersion()
		meta = map[string]interface{}{
			"io.modelcontextprotocol/clientCapabilities": map[string]interface{}{},
			"io.modelcontextprotocol/protocolVersion":    legacyVersion,
		}
		params["_meta"] = meta
		raw, err := json.Marshal(params)
		if err != nil {
			return ctx, "", jsonrpc.NewInvalidParamsError(err.Error(), request.Params)
		}
		request.Params = raw
		return ctx, legacyVersion, nil
	}
	version, _ := meta["io.modelcontextprotocol/protocolVersion"].(string)
	if version == "" {
		// July-generated parameter structs emit a zero-valued _meta object when
		// used by older in-process callers. Treat that shape as legacy rather
		// than mistaking it for an explicitly malformed July request.
		legacyVersion := h.legacyProtocolVersion()
		meta["io.modelcontextprotocol/protocolVersion"] = legacyVersion
		raw, err := json.Marshal(params)
		if err != nil {
			return ctx, "", jsonrpc.NewInvalidParamsError(err.Error(), request.Params)
		}
		request.Params = raw
		return ctx, legacyVersion, nil
	}
	if !supportedProtocolVersion(version) {
		data, _ := json.Marshal(map[string]interface{}{"requested": version, "supported": schema.SupportedProtocolVersions})
		return ctx, "", &jsonrpc.Error{Code: -32022, Message: fmt.Sprintf("unsupported MCP protocol version %q", version), Data: data}
	}
	if version != schema.LatestProtocolVersion {
		return ctx, version, nil
	}

	capabilities := schema.ClientCapabilities{}
	if value, ok := meta["io.modelcontextprotocol/clientCapabilities"]; ok {
		raw, _ := json.Marshal(value)
		if err := json.Unmarshal(raw, &capabilities); err != nil {
			return ctx, "", jsonrpc.NewInvalidParamsError("invalid io.modelcontextprotocol/clientCapabilities", request.Params)
		}
	}
	clientInfo := schema.Implementation{Name: "unknown", Version: "unknown"}
	if value, ok := meta["io.modelcontextprotocol/clientInfo"]; ok {
		raw, _ := json.Marshal(value)
		_ = json.Unmarshal(raw, &clientInfo)
	}
	var loggingLevel *schema.LoggingLevel
	if value, ok := meta["io.modelcontextprotocol/logLevel"]; ok {
		raw, _ := json.Marshal(value)
		var level schema.LoggingLevel
		if err := json.Unmarshal(raw, &level); err != nil {
			return ctx, "", jsonrpc.NewInvalidParamsError("invalid io.modelcontextprotocol/logLevel", request.Params)
		}
		loggingLevel = &level
	}
	requestInfo := &ProtocolRequest{Version: version, Capabilities: capabilities, ClientInfo: &clientInfo, LogLevel: loggingLevel}
	return withProtocolRequest(ctx, requestInfo), version, nil
}

func (h *Handler) legacyProtocolVersion() string {
	if h.clientInitialize != nil && h.clientInitialize.ProtocolVersion != "" {
		return h.clientInitialize.ProtocolVersion
	}
	return schema.LegacyProtocolVersion
}

func supportedProtocolVersion(version string) bool {
	for _, supported := range schema.SupportedProtocolVersions {
		if version == supported {
			return true
		}
	}
	return version == "2025-06-18"
}

func (h *Handler) Discover(ctx context.Context) (*schema.DiscoverResult, *jsonrpc.Error) {
	info := h.info
	result := &schema.DiscoverResult{
		Meta:              &schema.ResultMetaObject{IoModelcontextprotocolServerInfo: &info},
		CacheScope:        schema.DiscoverResultCacheScopePublic,
		Capabilities:      schema.ServerCapabilities{},
		Instructions:      h.instructions,
		ResultType:        completeResultType,
		SupportedVersions: append([]string(nil), schema.SupportedProtocolVersions...),
		TtlMs:             0,
	}
	if discoverer, ok := h.handler.(interface {
		Discover(context.Context, *schema.DiscoverResult)
	}); ok {
		discoverer.Discover(ctx, result)
	} else if requestInfo, ok := ProtocolRequestFromContext(ctx); ok {
		// Compatibility fallback for handlers that have not implemented the
		// stateless discovery hook yet.
		clientInfo := schema.Implementation{Name: "unknown", Version: "unknown"}
		if requestInfo.ClientInfo != nil {
			clientInfo = *requestInfo.ClientInfo
		}
		init := &schema.InitializeRequestParams{Capabilities: requestInfo.Capabilities, ClientInfo: clientInfo, ProtocolVersion: requestInfo.Version}
		compatResult := &schema.InitializeResult{Capabilities: result.Capabilities}
		h.handler.Initialize(ctx, init, compatResult)
		result.Capabilities = compatResult.Capabilities
	}
	return result, nil
}

func (h *Handler) finalizeResult(version string, result interface{}) {
	if result == nil || (reflect.ValueOf(result).Kind() == reflect.Ptr && reflect.ValueOf(result).IsNil()) {
		return
	}
	info := h.info
	var meta *schema.ResultMetaObject
	if version == schema.LatestProtocolVersion {
		meta = &schema.ResultMetaObject{IoModelcontextprotocolServerInfo: &info}
	}
	switch value := result.(type) {
	case *schema.ListToolsResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
		if value.CacheScope == "" {
			value.CacheScope = schema.ListToolsResultCacheScopePrivate
		}
	case *schema.ListResourcesResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
		if value.CacheScope == "" {
			value.CacheScope = schema.ListResourcesResultCacheScopePrivate
		}
	case *schema.ListResourceTemplatesResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
		if value.CacheScope == "" {
			value.CacheScope = schema.ListResourceTemplatesResultCacheScopePrivate
		}
	case *schema.ListPromptsResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
		if value.CacheScope == "" {
			value.CacheScope = schema.ListPromptsResultCacheScopePrivate
		}
	case *schema.ReadResourceResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
		if value.ResultType != schema.ResultTypeInputRequired && value.CacheScope == "" {
			value.CacheScope = schema.ReadResourceResultCacheScopePrivate
		}
	case *schema.CallToolResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
	case *schema.GetPromptResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
	case *schema.CompleteResult:
		if value.Meta == nil {
			value.Meta = meta
		}
		if value.ResultType == "" {
			value.ResultType = completeResultType
		}
	}
}
