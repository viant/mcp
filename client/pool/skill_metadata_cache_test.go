package manager

import (
	"context"
	format "github.com/viant/mcp-protocol/extension/skills"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
)

const skillCacheDocument = "---\nname: review\ndescription: Synthetic review\n---\nbody"

type skillCacheClient struct {
	catalogClient
	gets, reads atomic.Int32
	ttl         *int
	legacy      bool
	resourceTTL int
}

func (c *skillCacheClient) GetSkill(_ context.Context, uri string, _ ...mcpclient.RequestOption) (*schema.GetSkillResult, error) {
	c.gets.Add(1)
	compiled, err := (format.Compiler{Source: fstest.MapFS{"SKILL.md": {Data: []byte(skillCacheDocument)}}}).Compile(context.Background(), uri)
	if err != nil {
		return nil, err
	}
	value := &schema.GetSkillResult{ResultType: "complete", TtlMs: c.ttl, Skill: compiled.Metadata()}
	return value, nil
}
func (c *skillCacheClient) ListSkills(context.Context, *string, ...mcpclient.RequestOption) (*schema.ListSkillsResult, error) {
	return &schema.ListSkillsResult{}, nil
}
func (c *skillCacheClient) ReadResource(_ context.Context, p *schema.ReadResourceRequestParams, _ ...mcpclient.RequestOption) (*schema.ReadResourceResult, error) {
	c.reads.Add(1)
	result := &schema.ReadResourceResult{TtlMs: c.resourceTTL, CacheScope: schema.ReadResourceResultCacheScopePrivate, ResultType: "complete", Contents: []schema.ReadResourceResultContentsElem{{Uri: p.Uri, Text: skillCacheDocument}}}
	if c.legacy {
		result.CacheScope = ""
		result.ResultType = ""
	}
	return result, nil
}

func TestSkillMetadataReusesScopedCatalogAndInvalidatesPublication(t *testing.T) {
	m, _, _, a, b := catalogSetup(t, true)
	ttl := 60000
	client := &skillCacheClient{ttl: &ttl, resourceTTL: ttl}
	a.entry.client = client
	b.entry.client = client
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	uri := "skill://example/review/SKILL.md"
	for i := 0; i < 100; i++ {
		manifest, err := a.GetSkill(alice, uri)
		require.NoError(t, err)
		result, err := a.ReadSkillResource(alice, manifest, uri)
		require.NoError(t, err)
		require.Equal(t, skillCacheDocument, result.Contents[0].Text)
		result.Contents[0].Text = "caller mutation"
		manifest.Skill.Uri = "caller mutation"
	}
	require.EqualValues(t, 1, client.gets.Load())
	require.EqualValues(t, 1, client.reads.Load())
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	_, err := b.GetSkill(bob, uri)
	require.NoError(t, err)
	require.EqualValues(t, 2, client.gets.Load(), "public tools do not make skill documents public")
	m.notifyCatalogChange("server")
	manifest, err := a.GetSkill(alice, uri)
	require.NoError(t, err)
	_, err = a.ReadSkillResource(alice, manifest, uri)
	require.NoError(t, err)
	require.EqualValues(t, 3, client.gets.Load())
	require.EqualValues(t, 2, client.reads.Load())
	m.notifyResourceUpdated("server", [32]byte{}, true, uri)
	_, err = a.GetSkill(alice, uri)
	require.NoError(t, err)
	require.EqualValues(t, 4, client.gets.Load(), "exact resource update invalidates manifest alias")
}

func TestSkillMetadataHonorsExplicitZeroTTLAndRequestCredentials(t *testing.T) {
	_, _, _, a, _ := catalogSetup(t, false)
	zero := 0
	client := &skillCacheClient{ttl: &zero}
	a.entry.client = client
	uri := "skill://example/review/SKILL.md"
	for i := 0; i < 2; i++ {
		_, err := a.GetSkill(context.Background(), uri)
		require.NoError(t, err)
	}
	require.EqualValues(t, 2, client.gets.Load())
	ttl := 60000
	client.ttl = &ttl
	for _, token := range []string{"one", "one", "two"} {
		_, err := a.GetSkill(context.Background(), uri, mcpclient.WithAuthToken(token))
		require.NoError(t, err)
	}
	require.EqualValues(t, 4, client.gets.Load())
}

func TestSkillDocumentCacheDoesNotReviveClosedClient(t *testing.T) {
	m, _, _, a, _ := catalogSetup(t, false)
	source := &skillCacheClient{}
	a.entry.client = source
	ctx := context.Background()
	uri := "skill://example/review/SKILL.md"
	manifest, err := a.GetSkill(ctx, uri)
	require.NoError(t, err)
	_, err = a.ReadSkillResource(ctx, manifest, uri)
	require.NoError(t, err)
	m.mu.Lock()
	a.entry.closed = true
	m.mu.Unlock()
	_, err = a.ReadSkillResource(ctx, manifest, uri)
	require.Error(t, err)
	require.EqualValues(t, 1, source.reads.Load())
}

func TestSkillDocumentCachePreservesLegacyMissingHints(t *testing.T) {
	_, _, _, a, _ := catalogSetup(t, false)
	source := &skillCacheClient{legacy: true}
	a.entry.client = source
	ctx := context.Background()
	uri := "skill://example/review/SKILL.md"
	manifest, err := a.GetSkill(ctx, uri)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		result, err := a.ReadSkillResource(ctx, manifest, uri)
		require.NoError(t, err)
		require.Empty(t, result.CacheScope)
		require.Equal(t, skillCacheDocument, result.Contents[0].Text)
	}
	require.EqualValues(t, 2, source.reads.Load(), "missing freshness hints require revalidation")
}

func TestSkillMetadataRequiresProducerFreshnessForEachRead(t *testing.T) {
	_, _, _, managed, _ := catalogSetup(t, false)
	source := &skillCacheClient{}
	managed.entry.client = source
	ctx := context.Background()
	uri := "skill://example/review/SKILL.md"
	for i := 0; i < 2; i++ {
		manifest, err := managed.GetSkill(ctx, uri)
		require.NoError(t, err)
		_, err = managed.ReadSkillResource(ctx, manifest, uri)
		require.NoError(t, err)
	}
	require.EqualValues(t, 2, source.gets.Load())
	require.EqualValues(t, 2, source.reads.Load())
	ttl := 60000
	source.ttl = &ttl
	for i := 0; i < 2; i++ {
		manifest, err := managed.GetSkill(ctx, uri)
		require.NoError(t, err)
		_, err = managed.ReadSkillResource(ctx, manifest, uri)
		require.NoError(t, err)
	}
	require.EqualValues(t, 3, source.gets.Load())
	require.EqualValues(t, 4, source.reads.Load(), "manifest TTL must not override a zero resource TTL")
}
