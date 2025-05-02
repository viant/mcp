package flow

import (
	"context"
	"golang.org/x/oauth2"
)

type AuthFlow interface {
	Token(ctx context.Context, config *oauth2.Config, options ...Option) (*oauth2.Token, error)
}
