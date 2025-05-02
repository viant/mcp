package transport

import (
	"context"
	"github.com/viant/mcp/protocol/client/auth/flow"
	"strings"
)

type (
	contextRequestKey string
	contextScopeKey   string
)

const (
	ContextRequestKey    contextRequestKey = "request"
	ContextFlowOptionKey contextScopeKey   = "authFlowOptions"
)

func getAuthFlowOptions(ctx context.Context) []flow.Option {
	var options []flow.Option
	if value := ctx.Value(ContextFlowOptionKey); value != nil {
		options, _ = value.([]flow.Option)
	}
	return options
}

func getScope(ctx context.Context) string {
	options := getAuthFlowOptions(ctx)
	if len(options) == 0 {
		return ""
	}
	return strings.Join(flow.NewOptions(options).Scopes(), " ")
}
