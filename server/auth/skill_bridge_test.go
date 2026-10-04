package auth

import (
	"context"
	"fmt"
	"testing"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"
)

func TestSkillBridgeVerifiesCredentialsAndSupportingResourceScopes(t *testing.T) {
	root := "skill://private/SKILL.md"
	reference := "skill://private/references/guide.md"
	for _, global := range []bool{false, true} {
		policy := &authorization.Policy{Resources: map[string]*authorization.Authorization{
			root: {RequiredScopes: []string{"manifest:read"}}, reference: {RequiredScopes: []string{"reference:read"}},
		}}
		if global {
			policy.Global = &authorization.Authorization{RequiredScopes: []string{"all:read"}}
		}
		for _, name := range []string{schema.MethodSkillsList, schema.MethodSkillsGet} {
			for _, credential := range []string{"", "forged", "manifest-only", "verified"} {
				service, err := New(&Config{Policy: policy, RequireResourceAuthorization: true, AuthorizeResource: func(_ context.Context, token *authorization.Token, rule *authorization.Authorization) error {
					if token != nil && (token.Token == "verified" || !global && token.Token == "manifest-only" && rule.RequiredScopes[0] == "manifest:read") {
						return nil
					}
					return fmt.Errorf("denied")
				}})
				if err != nil {
					t.Fatal(err)
				}
				request, err := jsonrpc.NewRequest(schema.MethodToolsCall, map[string]any{"name": name, "arguments": map[string]any{"uri": root}})
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				if credential != "" {
					ctx = context.WithValue(ctx, authorization.TokenKey, &authorization.Token{Token: credential})
				}
				response := &jsonrpc.Response{}
				_, err = service.EnsureAuthorized(ctx, request, response)
				if err != nil {
					t.Fatal(err)
				}
				allowed := credential == "verified"
				if (response.Error == nil) != allowed {
					t.Fatalf("global=%v name=%s credential-kind=%s err=%v", global, name, credential, response.Error)
				}
			}
		}
	}
}
