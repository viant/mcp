package auth

import (
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp/schema"
)

type Authorizer func(request *jsonrpc.Request, response *jsonrpc.Response) *schema.AuthToken

// EnsureAuthorized   checks if a request is authorized.
func (s *AuthServer) EnsureAuthorized(request *jsonrpc.Request, response *jsonrpc.Response) *schema.AuthToken {
	if response.Error != nil {
		return nil
	}
	var p schema.WithAuthMeta
	if !schema.MustParseParams(request, response, &p) {
		return nil
	}

	var token string
	if p.AuthMeta.Credentials != nil {
		token = p.AuthMeta.Credentials.Token
	}
	hasToken := token != ""
	if hasToken {
		return p.AuthMeta.Credentials
	}

	if s.Config.Global != nil { //each request is protected
		s.unauthorized(response, s.Config.Global)
		return nil
	}

	switch request.Method {
	case schema.MethodToolsCall:
		if s.Config.Tools == nil {
			return nil
		}
		s.unauthorized(response, s.Config.Tools[p.Name])
	case schema.MethodResourcesRead:
		if s.Config.Tools == nil {
			return nil
		}
		s.unauthorized(response, s.Config.Tenants[p.Name])
	}
	return nil
}

func (s *AuthServer) unauthorized(resp *jsonrpc.Response, meta *schema.Authorization) {
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
