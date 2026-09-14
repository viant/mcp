package auth

import (
	"context"
	"encoding/json"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
	"sort"
	"strings"
)

// ResourceAuthorizer verifies credentials and required scopes for server-owned
// content. It must use the deployment's existing credential-verification owner;
// a nonempty token alone is not proof. No skill frontmatter enters this decision.
type ResourceAuthorizer func(context.Context, *authorization.Token, *authorization.Authorization) error

func (s *Service) resourceRules(request *jsonrpc.Request) []*authorization.Authorization {
	if s.Policy == nil {
		return nil
	}
	switch request.Method {
	case schema.MethodResourcesRead, schema.MethodResourcesList, schema.MethodResourcesTemplatesList, schema.MethodSkillsList, schema.MethodSkillsGet:
	default:
		return nil
	}
	if s.Policy.Global != nil {
		return []*authorization.Authorization{s.Policy.Global}
	}
	var params struct {
		Uri string `json:"uri"`
	}
	_ = json.Unmarshal(request.Params, &params)
	keys := []string{}
	for uri, rule := range s.Policy.Resources {
		if rule == nil {
			continue
		}
		match := request.Method == schema.MethodSkillsList || request.Method == schema.MethodResourcesList || request.Method == schema.MethodResourcesTemplatesList || uri == params.Uri
		if request.Method == schema.MethodSkillsGet && strings.HasSuffix(params.Uri, "/SKILL.md") {
			match = match || strings.HasPrefix(uri, strings.TrimSuffix(params.Uri, "SKILL.md"))
		}
		if match {
			keys = append(keys, uri)
		}
	}
	sort.Strings(keys)
	rules := make([]*authorization.Authorization, 0, len(keys))
	for _, key := range keys {
		rules = append(rules, s.Policy.Resources[key])
	}
	return rules
}

func (s *Service) authorizeResources(ctx context.Context, request *jsonrpc.Request, response *jsonrpc.Response, token *authorization.Token) bool {
	switch request.Method {
	case schema.MethodSkillsList, schema.MethodSkillsGet:
	case schema.MethodResourcesRead, schema.MethodResourcesList, schema.MethodResourcesTemplatesList:
		if !s.RequireResourceAuthorization {
			return false
		}
	default:
		return false
	}
	for _, rule := range s.resourceRules(request) {
		if s.AuthorizeResource == nil || s.AuthorizeResource(ctx, token, rule) != nil {
			s.unauthorized(response, rule)
			return true
		}
	}
	return true
}
