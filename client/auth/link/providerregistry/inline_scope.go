package providerregistry

import (
	"context"
	"fmt"

	authctx "github.com/viant/mcp/client/auth/link/internal/authcontext"
)

// inlineOwner uses only trusted ingress identity keys. No principal means a
// trusted startup registration; a partial authenticated context cannot silently
// become that global scope. Token strings are never parsed to infer ownership.
func inlineOwner(ctx context.Context) (string, error) {
	canonical, effective, provider := authctx.CanonicalUserID(ctx), authctx.EffectiveUserID(ctx), authctx.Provider(ctx)
	if canonical != "" && effective != "" {
		return canonical, nil
	}
	if canonical != "" || effective != "" || provider != "" || authctx.User(ctx) != nil || authctx.TokensFromContext(ctx) != nil || authctx.Bearer(ctx) != "" || authctx.IDToken(ctx) != "" {
		return "", fmt.Errorf("inline oauth provider requires verified canonical identity")
	}
	return "", nil
}

type inlineLookupOwnerKey struct{}

// WithInlineLookupOwner scopes provider READ lookup for an already validated
// canonical stored owner. It does not create authentication identity and cannot
// authorize registration. Background token refresh validates owner status first.
func WithInlineLookupOwner(ctx context.Context, canonical string) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, inlineLookupOwnerKey{}, canonical)
}
func lookupOwnerPresent(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(inlineLookupOwnerKey{}).(string)
	return ok
}
func inlineLookupOwner(ctx context.Context) (string, error) {
	if ctx != nil {
		if owner, ok := ctx.Value(inlineLookupOwnerKey{}).(string); ok {
			if owner == "" {
				return "", fmt.Errorf("inline provider lookup requires canonical owner")
			}
			ingress, err := inlineOwner(ctx)
			if err != nil {
				return "", err
			}
			if ingress != "" && ingress != owner {
				return "", fmt.Errorf("inline provider lookup owner differs from authenticated principal")
			}
			return owner, nil
		}
	}
	return inlineOwner(ctx)
}
