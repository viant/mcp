package link

import (
	"context"
	"errors"
	"time"
)

var ErrOAuthStateInvalid = errors.New("oauth link state is invalid")

type OAuthStateRecord struct {
	StateHash       string
	FlowHash        string
	CanonicalUserID string
	SessionHash     string
	Provider        string
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
	CreatedAt       time.Time
}

func (r *OAuthStateRecord) Pending(now time.Time) bool {
	return r != nil && r.ConsumedAt == nil && r.ExpiresAt.After(now)
}

type OAuthStateStore interface {
	CreateOrGetPending(context.Context, *OAuthStateRecord) (*OAuthStateRecord, bool, error)
	Consume(context.Context, string, string, string) error
	DeleteExpired(context.Context, time.Time) (int64, time.Time, error)
}
