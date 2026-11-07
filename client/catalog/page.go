// Package catalog provides immutable, indexed MCP tool metadata. It owns no
// credentials, transport, retention policy, or agent selection policy.
package catalog

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/tiendc/go-deepcopy"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpname "github.com/viant/mcp/client/toolname"
)

// Page owns a decoded tools/list page; returned values are detached copies.
type Page struct {
	result *mcpschema.ListToolsResult
	index  map[string][]int
	digest [32]byte
}

// New parses a wire page once and indexes literal names and provider aliases.
func New(wire []byte, servers ...string) (*Page, error) {
	result, err := decode(wire)
	if err != nil {
		return nil, err
	}
	index := map[string][]int{}
	for i, tool := range result.Tools {
		aliases := map[string]bool{tool.Name: true}
		for _, server := range servers {
			for _, full := range []string{server + "/" + tool.Name, server + ":" + tool.Name} {
				aliases[full] = true
				aliases[mcpname.Canonical(full)] = true
			}
		}
		for alias := range aliases {
			index[alias] = append(index[alias], i)
		}
	}
	return &Page{result: result, index: index, digest: sha256.Sum256(wire)}, nil
}

func (p *Page) Digest() [32]byte { return p.digest }
func (p *Page) NextCursor() string {
	if p.result.NextCursor == nil {
		return ""
	}
	return *p.result.NextCursor
}
func (p *Page) Copy() (*mcpschema.ListToolsResult, error) {
	var result mcpschema.ListToolsResult
	if err := deepcopy.Copy(&result, p.result, deepcopy.CopyViaCopyingMethod(false)); err != nil {
		return nil, err
	}
	return &result, nil
}

// Select returns only requested names/aliases. A nil names slice selects all
// tools; an empty non-nil slice selects none. Metadata excludes both schemas.
func (p *Page) Select(names []string, metadataOnly bool) ([]mcpschema.Tool, error) {
	var selected []mcpschema.Tool
	appendTool := func(i int) {
		tool := p.result.Tools[i]
		if metadataOnly {
			tool.InputSchema = mcpschema.ToolInputSchema{}
			tool.OutputSchema = nil
		}
		selected = append(selected, tool)
	}
	if names == nil {
		for i := range p.result.Tools {
			appendTool(i)
		}
	} else {
		seen := map[int]bool{}
		for _, name := range names {
			for _, i := range p.index[name] {
				if !seen[i] {
					seen[i] = true
					appendTool(i)
				}
			}
		}
	}
	var detached []mcpschema.Tool
	if err := deepcopy.Copy(&detached, &selected, deepcopy.CopyViaCopyingMethod(false)); err != nil {
		return nil, err
	}
	return detached, nil
}

func decode(payload []byte) (*mcpschema.ListToolsResult, error) {
	type plain mcpschema.ListToolsResult
	var result plain
	wire := struct {
		*plain
		CacheScope string `json:"cacheScope"`
	}{plain: &result}
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, err
	}
	result.CacheScope = mcpschema.ListToolsResultCacheScope(wire.CacheScope)
	return (*mcpschema.ListToolsResult)(&result), nil
}
