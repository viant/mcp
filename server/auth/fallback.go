package auth

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	"strings"
)

// FallbackAuth is a fallback authorization interceptor
type FallbackAuth struct {
	Strict        *AuthServer
	TokenSource   authorization.ProtectedResourceTokenSource
	IdTokenSource authorization.IdTokenSource
}

func (a *FallbackAuth) EnsureAuthorized(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) (*authorization.Token, error) {
	token, err := a.Strict.EnsureAuthorized(ctx, request, response)
	if token != nil {
		return token, nil
	}
	if response.Error == nil || response.Error.Code != schema.Unauthorized || response.Error.Data == nil {
		return nil, nil
	}

	var anAuthorization authorization.Authorization
	if err = json.Unmarshal(response.Error.Data, &anAuthorization); err != nil {
		return nil, err
	}
	oToken, err := a.TokenSource.ProtectedResourceToken(ctx, anAuthorization.ProtectedResourceMetadata, strings.Join(anAuthorization.RequiredScopes, " "))
	if err != nil {
		return nil, err
	}

	if anAuthorization.UseIdToken {
		oToken, err = a.IdTokenSource.IdToken(ctx, oToken, anAuthorization.ProtectedResourceMetadata)
		if err != nil {
			return nil, err
		}
	}
	tokenString := oToken.AccessToken
	token = &authorization.Token{
		Token: tokenString,
	}
	response.Error = nil
	return token, nil
}

func NewFallbackAuth(authServer *AuthServer, tokenSource authorization.ProtectedResourceTokenSource, idTokenSource authorization.IdTokenSource) *FallbackAuth {
	return &FallbackAuth{
		Strict:        authServer,
		TokenSource:   tokenSource,
		IdTokenSource: idTokenSource,
	}
}
