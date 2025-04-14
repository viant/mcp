package client

import (
	"context"
	"github.com/viant/jsonrpc"
)

type Implementer interface {
	Operations

	OnNotification(ctx context.Context, notification *jsonrpc.Notification)

	// Implements checks if the method is implemented
	Implements(method string) bool
}
