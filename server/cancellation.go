package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

func (h *Handler) Cancel(ctx context.Context, notification *jsonrpc.Notification) *jsonrpc.Error {
	request := schema.CancelledNotification{Method: notification.Method}
	if err := json.Unmarshal(notification.Params, &request); err != nil {
		return jsonrpc.NewParsingError(fmt.Sprintf("failed to parse notificaiton: %v", err), notification.Params)
	}
	if request.Params.RequestId == 0 {
		return jsonrpc.NewInvalidParamsError("invalid requestId", notification.Params)
	}
	h.CancelOperation(int(request.Params.RequestId))
	return nil
}
