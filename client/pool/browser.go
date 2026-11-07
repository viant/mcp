// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"errors"
	"sort"

	authctx "github.com/viant/mcp/client/auth/link"
	mcpcfg "github.com/viant/mcp/client/config"
)

var ErrBrowserExecutionRequired = errors.New("MCP server requires its originating browser connection; server fallback is disabled")

func (m *Manager) BrowserDescriptors(ctx context.Context) ([]mcpcfg.BrowserDescriptor, error) {
	if authctx.EffectiveUserID(ctx) == "" {
		return nil, errors.New("authenticated browser configuration required")
	}
	lister, ok := m.prov.(providerLister)
	if !ok {
		return []mcpcfg.BrowserDescriptor{}, nil
	}
	names, err := lister.Names(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	result := []mcpcfg.BrowserDescriptor{}
	for _, name := range names {
		c, err := browserOptions(ctx, m.prov, name)
		if err != nil {
			return nil, err
		}
		if c == nil {
			continue
		}
		if err = c.ValidateExecutionLocation(); err != nil {
			return nil, err
		}
		if !c.IsBrowserExecution() {
			continue
		}
		d, err := c.BrowserDescriptor(name)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}

func browserOptions(ctx context.Context, p Provider, name string) (*mcpcfg.MCPClient, error) {
	if raw, ok := p.(interface {
		BrowserOptions(context.Context, string) (*mcpcfg.MCPClient, error)
	}); ok {
		return raw.BrowserOptions(ctx, name)
	}
	return p.Options(ctx, name)
}

// BrowserDescriptor revalidates one already-registered browser server without
// enumerating or loading unrelated server configurations or credential state.
func (m *Manager) BrowserDescriptor(ctx context.Context, name string) (mcpcfg.BrowserDescriptor, error) {
	if authctx.EffectiveUserID(ctx) == "" || name == "" {
		return mcpcfg.BrowserDescriptor{}, errors.New("authenticated browser configuration required")
	}
	c, err := browserOptions(ctx, m.prov, name)
	if err != nil {
		return mcpcfg.BrowserDescriptor{}, err
	}
	if c == nil || c.ValidateExecutionLocation() != nil || !c.IsBrowserExecution() {
		return mcpcfg.BrowserDescriptor{}, ErrBrowserExecutionRequired
	}
	return c.BrowserDescriptor(name)
}
