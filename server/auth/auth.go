package auth

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	authschema "github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
)

// Authorizer is an interceptor function for JSON-RPC calls that returns
// a Token when authorization is successful or nil otherwise.
type Authorizer func(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) (*authschema.Token, error)

// EnsureAuthorized checks if a request is authorized.
func (s *AuthServer) EnsureAuthorized(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) (*authschema.Token, error) {
	if response.Error != nil {
		return nil, nil
	}

	if s.Config.Global != nil {
		s.unauthorized(response, s.Config.Global)
		return nil, nil
	}

	var p authschema.WithMeta
	// Parse the JSON-RPC params into the WithAuthMeta wrapper
	if !schema.MustParseParams(request, response, &p) {
		return nil, nil
	}

	var token string
	if p.AuthMeta.Authorization != nil {
		token = p.AuthMeta.Authorization.Token
	}
	hasToken := token != ""
	if hasToken {
		return p.AuthMeta.Authorization, nil
	}

	if s.Config.Global != nil { //each request is protected
		s.unauthorized(response, s.Config.Global)
		return nil, nil
	}

	switch request.Method {
	case schema.MethodToolsCall:
		if s.Config.Tools == nil {
			return nil, nil
		}
		s.unauthorized(response, s.Config.Tools[p.Name])
	case schema.MethodResourcesRead:
		if s.Config.Tenants == nil {
			return nil, nil
		}
		s.unauthorized(response, s.Config.Tenants[p.Name])
	}
	return nil, nil
}

func (s *AuthServer) unauthorized(resp *jsonrpc.Response, meta *authschema.Authorization) {
	if meta == nil {
		return // the tool/resource isn’t protected → silently allow
	}
	data, _ := json.Marshal(meta)
	resp.Error = &jsonrpc.Error{
		Code:    schema.Unauthorized,
		Message: "Unauthorized: protected resource requires authorization",
		Data:    data,
	}

}
