package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

func (h *Handler) Cancel(ctx context.Context, notification *jsonrpc.Notification) *jsonrpc.Error {
	var params schema.CancelledNotificationParams
	if err := json.Unmarshal(notification.Params, &params); err != nil {
		return jsonrpc.NewParsingError(fmt.Sprintf("failed to parse notificaiton: %v", err), notification.Params)
	}
	if params.RequestId == nil || *params.RequestId == 0 {
		return jsonrpc.NewInvalidParamsError("invalid requestId", notification.Params)
	}
	h.CancelOperation(int(*params.RequestId))
	return nil
}
