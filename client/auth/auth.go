package auth

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client/auth/transport"
	"golang.org/x/oauth2"
	"strings"
)

// Authorizer is a fine grain authorization interceptor
type Authorizer struct { //fine grain authorization - experimental implementation - it's not part of the spec
	Transport *transport.RoundTripper
}

// Intercept intercept to check is authorization is needed, and initiate oauth 2.1 to get token and inject it to the request to re-send
func (a *Authorizer) Intercept(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) (*jsonrpc.Request, error) {
	if response.Error == nil {
		return nil, nil
	}
	if response.Error.Code == schema.Unauthorized {
		data, _ := json.Marshal(response.Error.Data)
		if len(data) == 0 {
			return nil, nil //unable to get PRM document
		}
		var anAuthorization authorization.Authorization
		if err := json.Unmarshal(data, &anAuthorization); err != nil {
			return nil, err
		}
		token, err := a.Transport.ProtectedResourceToken(ctx, anAuthorization.ProtectedResourceMetadata, strings.Join(anAuthorization.RequiredScopes, " "))
		if err != nil {
			return nil, err
		}
		if anAuthorization.UseIdToken {
			token, err = a.Transport.IdToken(ctx, token, anAuthorization.ProtectedResourceMetadata)
			if err != nil {
				return nil, err
			}
		}
		next, err := injectToken(request, token)
		if err != nil {
			return nil, err
		}
		return next, nil
	}
	return nil, nil
}

func injectToken(request *jsonrpc.Request, token *oauth2.Token) (*jsonrpc.Request, error) {
	params := map[string]interface{}{}
	err := json.Unmarshal([]byte(request.Params), &params)
	if err != nil {
		return nil, err
	}
	var paramMeta map[string]interface{}
	metaValue, ok := params["_meta"]
	if ok {
		paramMeta = metaValue.(map[string]interface{})
	} else {
		paramMeta = make(map[string]interface{})
		params["_meta"] = paramMeta
	}
	var authorizationMetadata map[string]interface{}
	authorizationValue, ok := paramMeta["authorization"]
	if ok {
		authorizationMetadata = authorizationValue.(map[string]interface{})
	} else {
		authorizationMetadata = make(map[string]interface{})
		paramMeta["authorization"] = authorizationMetadata
	}

	accessTokenValue := token.AccessToken
	authorizationMetadata["token"] = accessTokenValue
	next := *request
	if next.Params, err = json.Marshal(params); err != nil {
		return nil, err
	}
	return &next, nil
}

func NewAuthorizer(transport *transport.RoundTripper) *Authorizer {
	return &Authorizer{
		Transport: transport,
	}
}
