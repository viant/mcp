package transport

import (
	"context"
	"github.com/viant/scy/auth/flow"
	"strings"
)

type (
	contextScopeKey string
)

const (
	ContextFlowOptionKey contextScopeKey = "authFlowOptions"
)

func getAuthFlowOptions(ctx context.Context) []flow.Option {
	var options []flow.Option
	if value := ctx.Value(ContextFlowOptionKey); value != nil {
		options, _ = value.([]flow.Option)
	}
	options = append(options, flow.WithPKCE(true))
	return options
}

func getScope(ctx context.Context) string {
	options := getAuthFlowOptions(ctx)
	if len(options) == 0 {
		return ""
	}
	return strings.Join(flow.NewOptions(options).Scopes(), " ")
}
