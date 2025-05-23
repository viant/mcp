package auth

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
)

// JRPCAuthorizer is an interceptor function for JSON-RPC calls that returns
// a Token when authorization is successful or nil otherwise.
type JRPCAuthorizer func(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) (*authorization.Token, error)

// EnsureAuthorized checks if a request is authorized.
func (s *Service) EnsureAuthorized(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response) (*authorization.Token, error) {
	if response.Error != nil {
		return nil, nil
	}

	var p authorization.WithMeta
	// Parse the JSON-RPC params into the WithAuthMeta wrapper
	if !schema.MustParseParams(request, response, &p) {
		return nil, nil
	}

	var token string
	if value := ctx.Value(authorization.TokenKey); value != nil {
		if _, ok := value.(*authorization.Token); ok { //token is already in context
			return nil, nil
		}
	}
	if p.AuthMeta.Authorization != nil {
		token = p.AuthMeta.Authorization.Token
	}
	hasToken := token != ""
	if hasToken {
		return p.AuthMeta.Authorization, nil
	}

	switch request.Method {
	case schema.MethodToolsCall:
		if s.Policy.Tools == nil {
			if s.Policy.Global != nil { //each request is protected
				s.unauthorized(response, s.Policy.Global)
			}
			return nil, nil
		}
		s.unauthorized(response, s.Policy.Tools[p.Name])
	case schema.MethodResourcesRead:
		if s.Policy.Resources == nil {
			if s.Policy.Global != nil { //each request is protected
				s.unauthorized(response, s.Policy.Global)
			}
			return nil, nil
		}
		s.unauthorized(response, s.Policy.Resources[p.Name])
	}
	return nil, nil
}

func (s *Service) unauthorized(resp *jsonrpc.Response, meta *authorization.Authorization) {
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
