package requestctx

import (
	"context"
	"github.com/google/uuid"
)

type conversationIDKey string

var ConversationIDKey = conversationIDKey("conversationID")

func ConversationIDFromContext(ctx context.Context) string {
	value := ctx.Value(ConversationIDKey)
	if value == nil {
		return ""
	}
	if id, ok := value.(string); ok {
		return id
	}
	return ""
}

func WithConversationID(ctx context.Context, conversationID string) context.Context {
	if conversationID == "" {
		conversationID = uuid.New().String()
	}
	prev := ctx.Value(ConversationIDKey)
	if prev != nil && prev == conversationID {
		return ctx
	}
	return context.WithValue(ctx, ConversationIDKey, conversationID)
}
