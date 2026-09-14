package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp-protocol/authorization"
	skillformat "github.com/viant/mcp-protocol/extension/skills"
	"github.com/viant/mcp-protocol/schema"
	protocol "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/server/auth"
)

// This exercises the native initialized client, JSON serialization and dispatch
// without a TCP listener. Content reads are counted separately from metadata.
type skillsTransport struct {
	handler transport.Handler
	id      atomic.Uint64
	methods []string
	results []json.RawMessage
}

func (s *skillsTransport) Notify(ctx context.Context, n *jsonrpc.Notification) error {
	s.handler.OnNotification(ctx, n)
	return nil
}
func (s *skillsTransport) Send(ctx context.Context, r *jsonrpc.Request) (*jsonrpc.Response, error) {
	r.Id = s.id.Add(1)
	response := &jsonrpc.Response{}
	s.handler.Serve(ctx, r, response)
	s.methods = append(s.methods, r.Method)
	s.results = append(s.results, response.Result)
	return response, nil
}

func TestSkillsInitializedProtocol(t *testing.T) {
	for _, version := range []string{"2025-11-25", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			ctx := context.Background()
			reads := 0
			factory := protocol.WithDefaultHandler(ctx, func(d *protocol.DefaultHandler) error {
				for i := 0; i < 35; i++ {
					// Same labels at distinct URIs must not collapse.
					root := fmt.Sprintf("docs://team%d/demo", i)
					body := []byte("---\nname: demo\ndescription: Demonstration.\nallowed-tools: Bash\nfuture: {flag: true}\n---\nRead needed.txt only when needed.")
					files := fstest.MapFS{"SKILL.md": {Data: body}, "needed.txt": {Data: []byte("needed")}, "unused.txt": {Data: []byte("never fetched")}}
					entry, err := (skillformat.Compiler{Source: files}).Compile(ctx, root+"/SKILL.md")
					if err != nil {
						return err
					}
					for name, file := range files {
						uri, data := root+"/"+name, string(file.Data)
						d.RegisterResource(schema.Resource{Name: uri, Uri: uri}, func(ctx context.Context, r *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
							reads++
							return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: uri, Text: data}}}, nil
						})
					}
					if err = d.RegisterStaticSkill(entry); err != nil {
						return err
					}
				}
				return nil
			})
			srv, err := New(WithNewHandler(factory))
			require.NoError(t, err)
			wire := &skillsTransport{}
			wire.handler = srv.NewHandler(ctx, wire)
			c := client.New("test", "1", wire, client.WithProtocolVersion(version))
			defer c.Close()
			init, err := c.Initialize(ctx)
			require.NoError(t, err)
			require.Contains(t, init.Capabilities.Extensions, schema.SkillsExtension)
			require.Empty(t, init.Capabilities.Extensions[schema.SkillsExtension])
			require.NotNil(t, init.Capabilities.Resources)
			first, err := c.ListSkills(ctx, nil)
			require.NoError(t, err)
			require.Len(t, first.Skills, 32)
			require.NotNil(t, first.NextCursor)
			raw := wire.results[len(wire.results)-1]
			if version == "2026-07-28" {
				require.Contains(t, string(raw), `"ttlMs":0`)
				require.Contains(t, string(raw), `"cacheScope":"private"`)
			} else {
				require.NotContains(t, string(raw), `"ttlMs"`)
				require.NotContains(t, string(raw), `"cacheScope"`)
			}
			second, err := c.ListSkills(ctx, first.NextCursor)
			require.NoError(t, err)
			require.Len(t, second.Skills, 3)
			require.Nil(t, second.NextCursor)
			got, err := c.GetSkill(ctx, first.Skills[0].Uri)
			require.NoError(t, err)
			require.Equal(t, first.Skills[0], got.Skill)
			require.Zero(t, reads)
			selected := got.Skill.Resources.Files[0]
			read, err := c.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: selected.Uri})
			require.NoError(t, err)
			require.Equal(t, selected.Size, int64(len(read.Contents[0].Text)))
			require.Equal(t, selected.Digest, fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(read.Contents[0].Text))))
			require.Zero(t, reads, "arbitrary registered handler must not supply static skill bytes")
			readRequests := 0
			for _, method := range wire.methods {
				if method == schema.MethodResourcesRead {
					readRequests++
				}
			}
			require.Equal(t, 1, readRequests, "metadata methods must not prefetch skill contents")
			bad := "not-a-cursor"
			_, err = c.ListSkills(ctx, &bad)
			require.Error(t, err)
			for _, uri := range []string{"", "docs://team0/demo/needed.txt", "skill://unknown/SKILL.md", "docs://team0/demo/SKILL.md?bad"} {
				_, err = c.GetSkill(ctx, uri)
				rpc, ok := err.(*jsonrpc.Error)
				require.True(t, ok)
				require.EqualValues(t, -32602, rpc.Code)
			}
			for _, tc := range []struct {
				method, params string
				code           int
			}{
				{schema.MethodSkillsGet, `{"uri":12}`, -32602}, {schema.MethodSkillsGet, `{}`, -32602}, {schema.MethodSkillsList, `{"cursor":12}`, -32602}, {"resources/directory/read", `{"uri":"docs://team0/demo"}`, -32601},
				{schema.MethodSkillsList, `{"cursor":null}`, -32602},
				{schema.MethodSkillsList, `{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}`, -32022},
			} {
				req := &jsonrpc.Request{Jsonrpc: "2.0", Method: tc.method, Params: json.RawMessage(tc.params)}
				resp, err := wire.Send(ctx, req)
				require.NoError(t, err)
				require.NotNil(t, resp.Error)
				require.EqualValues(t, tc.code, resp.Error.Code)
			}
			require.Zero(t, reads)
		})
	}
}

func TestSkillsOrdinaryResourcesDoNotAdvertise(t *testing.T) {
	ctx := context.Background()
	srv, err := New(WithNewHandler(protocol.WithDefaultHandler(ctx, func(d *protocol.DefaultHandler) error {
		d.RegisterResource(schema.Resource{Name: "document", Uri: "skill://ordinary/SKILL.md"}, nil)
		return nil
	})))
	require.NoError(t, err)
	wire := &skillsTransport{}
	wire.handler = srv.NewHandler(ctx, wire)
	c := client.New("ordinary", "1", wire)
	defer c.Close()
	init, err := c.Initialize(ctx)
	require.NoError(t, err)
	require.NotContains(t, init.Capabilities.Extensions, schema.SkillsExtension)
	_, err = c.ListSkills(ctx, nil)
	rpc, ok := err.(*jsonrpc.Error)
	require.True(t, ok)
	require.EqualValues(t, -32601, rpc.Code)
}

func TestSkillsPrivateMetadataAndContent(t *testing.T) {
	ctx := context.Background()
	root := "skill://private"
	files := fstest.MapFS{"SKILL.md": {Data: []byte("---\nname: private\ndescription: Secret instructions.\n---\nsecret")}, "private.txt": {Data: []byte("secret-file")}}
	entry, err := (skillformat.Compiler{Source: files}).Compile(ctx, root+"/SKILL.md")
	require.NoError(t, err)
	rule := &authorization.Authorization{RequiredScopes: []string{"private"}}
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			config := &auth.Config{Policy: &authorization.Policy{Resources: map[string]*authorization.Authorization{root + "/private.txt": rule, root + "/SKILL.md": rule}}, RequireResourceAuthorization: true}
			if configured {
				config.AuthorizeResource = func(_ context.Context, token *authorization.Token, actual *authorization.Authorization) error {
					require.Equal(t, rule, actual)
					if token == nil || token.Token != "verified-by-operator" {
						return fmt.Errorf("invalid credentials")
					}
					return nil
				}
			}
			authorizer, err := auth.New(config)
			require.NoError(t, err)
			srv, err := New(WithJRPCAuthorizer(authorizer.EnsureAuthorized), WithNewHandler(protocol.WithDefaultHandler(ctx, func(d *protocol.DefaultHandler) error {
				for name, file := range files {
					uri, data := root+"/"+name, string(file.Data)
					d.RegisterResource(schema.Resource{Name: uri, Uri: uri}, func(context.Context, *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
						return &schema.ReadResourceResult{Contents: []schema.ReadResourceResultContentsElem{{Uri: uri, Text: data}}}, nil
					})
				}
				return d.RegisterStaticSkill(entry)
			})))
			require.NoError(t, err)
			wire := &skillsTransport{}
			wire.handler = srv.NewHandler(ctx, wire)
			c := client.New("private", "1", wire)
			defer c.Close()
			_, err = c.Initialize(ctx)
			require.NoError(t, err)
			for _, token := range []string{"", "forged", "verified-by-operator"} {
				for _, method := range []string{schema.MethodSkillsList, schema.MethodSkillsGet, schema.MethodResourcesRead} {
					params := map[string]interface{}{"uri": entry.Metadata().Uri, "_meta": map[string]interface{}{"authorization": map[string]interface{}{"token": token}}}
					request, _ := jsonrpc.NewRequest(method, params)
					response, err := wire.Send(ctx, request)
					require.NoError(t, err)
					if configured && token == "verified-by-operator" {
						require.Nil(t, response.Error)
					} else {
						require.NotNil(t, response.Error)
						require.NotContains(t, string(response.Result), "secret")
					}
				}
			}
		})
	}
}
