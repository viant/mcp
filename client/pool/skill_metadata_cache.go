package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
)

var errSkillNotRetained = errors.New("skill metadata is not retained")

// skillMetadata uses the existing scoped, bounded, singleflight metadata store.
// Explicit non-cacheable responses are delivered without retaining their bytes.
func (c *managedClient) skillMetadata(ctx context.Context, entryKey string, load func(context.Context) (json.RawMessage, *int, error), opts ...mcpclient.RequestOption) (json.RawMessage, error) {
	scoped, config, scope, witness, token, cacheable, err := c.toolCatalogScope(ctx, opts...)
	if err != nil {
		return nil, err
	}
	if !cacheable {
		v, _, err := load(scoped)
		return v, err
	}
	// Skill metadata is always private, even when tools/list is public.
	private, _ := json.Marshal([]string{c.key, scope, authctx.EffectiveUserID(scoped), authctx.CanonicalUserID(scoped), authctx.Provider(scoped), authctx.Bearer(scoped), authctx.IDToken(scoped)})
	c.mgr.mu.Lock()
	generation := c.mgr.toolCatalogGeneration[c.serverName]
	c.mgr.mu.Unlock()
	key := fmt.Sprintf("skill:%p:%s:%x:%x:%d", c.entry, c.serverName, sha256.Sum256(private), sha256.Sum256(config), generation)
	partition := c.mgr.WithCatalogSources(scoped, c.mgr.CatalogPartition(scoped, scope, "skill"), []string{c.serverName})
	var unretained json.RawMessage
	raw, err := c.mgr.VisibleCatalogEntryInPartition(scoped, key, partition, entryKey, func(ctx context.Context) (json.RawMessage, time.Time, error) {
		value, ttl, err := load(ctx)
		if err != nil {
			return nil, time.Time{}, err
		}
		_, freshConfig, freshScope, _, _, freshCacheable, scopeErr := c.toolCatalogScope(ctx, opts...)
		valid := scopeErr == nil && freshCacheable && freshScope == scope && bytes.Equal(config, freshConfig)
		if witness != nil {
			witness.mu.Lock()
			valid = valid && witness.observed && witness.token == token
			witness.mu.Unlock()
		}
		if !valid {
			return nil, time.Time{}, fmt.Errorf("skill metadata credential changed during read")
		}
		if ttl != nil && *ttl <= 0 {
			unretained = value
			return nil, time.Time{}, errSkillNotRetained
		}
		duration := 5 * time.Minute
		if ttl != nil && time.Duration(*ttl)*time.Millisecond < duration {
			duration = time.Duration(*ttl) * time.Millisecond
		}
		return value, time.Now().Add(duration), nil
	})
	if errors.Is(err, errSkillNotRetained) {
		return unretained, nil
	}
	return raw, err
}

// ReadSkillResource lazily retains only immutable files declared by the supplied
// skill manifest. Other resources retain normal uncached ReadResource behavior.
func (c *managedClient) ReadSkillResource(ctx context.Context, manifest *schema.GetSkillResult, uri string, opts ...mcpclient.RequestOption) (*schema.ReadResourceResult, error) {
	_, release, err := c.use()
	if err != nil {
		return nil, err
	}
	defer release()
	if manifest == nil || manifest.ResultType != "complete" || manifest.Skill.Resources.Dynamic {
		return c.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: uri}, opts...)
	}
	declared := false
	for _, file := range manifest.Skill.Resources.Files {
		if file.Uri == uri && file.Digest != "" {
			declared = true
			break
		}
	}
	if !declared {
		return c.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: uri}, opts...)
	}
	revision, err := json.Marshal(manifest.Skill)
	if err != nil {
		return nil, err
	}
	raw, err := c.skillMetadata(ctx, fmt.Sprintf("%s\x00document:%x", uri, sha256.Sum256(revision)), func(ctx context.Context) (json.RawMessage, *int, error) {
		result, err := c.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: uri}, opts...)
		if err != nil {
			return nil, nil, err
		}
		if result == nil {
			zero := 0
			return json.RawMessage("null"), &zero, nil
		}
		raw, err := json.Marshal(result)
		return raw, manifest.TtlMs, err
	}, opts...)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	// In-process and older clients may omit cacheScope. The cache must preserve
	// that already-decoded value, not impose newer wire enum validation.
	type resourceValue schema.ReadResourceResult
	value := new(resourceValue)
	decoded := struct {
		*resourceValue
		CacheScope string `json:"cacheScope"`
	}{resourceValue: value}
	err = json.Unmarshal(raw, &decoded)
	if err != nil {
		return nil, err
	}
	value.CacheScope = schema.ReadResourceResultCacheScope(decoded.CacheScope)
	return (*schema.ReadResourceResult)(value), nil
}
