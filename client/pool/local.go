// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	mcpclient "github.com/viant/mcp/client"
	mcpcfg "github.com/viant/mcp/client/config"
	"strings"
)

type localRegistration struct {
	options *mcpcfg.MCPClient
	factory func(context.Context) (mcpclient.Interface, error)
}

// IsLocalClient reports an explicit process-owned registration. Names alone
// never establish this boundary; colliding external configuration still fails
// the manager's normal Get collision checks.
func (m *Manager) IsLocalClient(name string) bool {
	if m == nil {
		return false
	}
	_, ok := m.localRegistration(strings.TrimSpace(name))
	return ok
}

func (m *Manager) localRegistration(name string) (localRegistration, bool) {
	m.localMu.RLock()
	defer m.localMu.RUnlock()
	registration, ok := m.local[name]
	return registration, ok
}
func cloneLocalConfig(config *mcpcfg.MCPClient) *mcpcfg.MCPClient {
	raw, _ := json.Marshal(config)
	var copied mcpcfg.MCPClient
	_ = json.Unmarshal(raw, &copied)
	return &copied
}

// RegisterLocal reserves one exact connection name and uses ordinary SDK
// clients and the manager's existing per-user pools. Names may not shadow a
// configured external connection. The factory must not capture caller authority;
// each operation's request context must reach the local authenticated handler.
func (m *Manager) RegisterLocal(ctx context.Context, name string, options *mcpcfg.MCPClient, factory func(context.Context) (mcpclient.Interface, error)) error {
	if m == nil || ctx == nil || ctx.Err() != nil || name == "" || strings.TrimSpace(name) != name || options == nil || options.ClientOptions == nil || factory == nil {
		return fmt.Errorf("invalid local MCP registration")
	}
	if options.Auth != nil {
		return fmt.Errorf("local MCP registration cannot contain transport credentials")
	}
	if options.PrimitiveProviderIdentity == "" {
		return fmt.Errorf("local MCP registration requires declared provider identity")
	}
	m.definitionBindMu.Lock()
	defer m.definitionBindMu.Unlock()
	if provider, ok := m.prov.(interface{ DefinitionReaderBound() bool }); ok && provider.DefinitionReaderBound() {
		return fmt.Errorf("local MCP registration must precede definition reader binding")
	}
	// Require inventory when a provider exists: checking Options alone cannot
	// distinguish a missing entry from providers with default configurations.
	m.localMu.Lock()
	defer m.localMu.Unlock()
	if _, exists := m.local[name]; exists {
		return fmt.Errorf("duplicate local MCP connection %q", name)
	}
	if m.prov != nil {
		lister, ok := m.prov.(providerLister)
		if !ok {
			return fmt.Errorf("local MCP registration requires external connection inventory")
		}
		names, err := lister.Names(ctx)
		if err != nil {
			return err
		}
		for _, external := range names {
			if external == name {
				return fmt.Errorf("local MCP connection collides with configured provider %q", name)
			}
		}
	}
	if m.local == nil {
		m.local = map[string]localRegistration{}
	}
	copied := cloneLocalConfig(options)
	if copied.ClientOptions == nil {
		return fmt.Errorf("local MCP metadata must be serializable")
	}
	copied.Name = name
	copied.ToolsListVisibility = mcpcfg.ToolsListVisibilityPrivate
	m.mu.Lock()
	for _, pending := range m.inflight {
		if _, exists := pending[name]; exists {
			m.mu.Unlock()
			return fmt.Errorf("local MCP connection already has an inflight transport %q", name)
		}
	}
	for _, pool := range m.pool {
		if entry := pool[name]; entry != nil {
			m.mu.Unlock()
			return fmt.Errorf("local MCP connection already has a pooled transport %q", name)
		}
	}
	m.mu.Unlock()
	m.local[name] = localRegistration{options: copied, factory: factory}
	return nil
}

func (m *Manager) checkLocalCollision(ctx context.Context, name string) error {
	if provider, ok := m.prov.(interface{ ReservedLocalName(string) bool }); ok && provider.ReservedLocalName(name) {
		return nil
	}
	if m.prov == nil {
		return nil
	}
	lister, ok := m.prov.(providerLister)
	if !ok {
		return fmt.Errorf("local MCP connection inventory unavailable")
	}
	names, err := lister.Names(ctx)
	if err != nil {
		return err
	}
	for _, external := range names {
		if external == name {
			return fmt.Errorf("local MCP connection collides with configured provider %q", name)
		}
	}
	return nil
}

// LocalProviderNames returns only process-owned registrations for a declared
// primitive owner. It does not initialize a client or read a provider endpoint.
// Configured external connections are deliberately not classified as local.
func (m *Manager) LocalProviderNames(ctx context.Context, owner string) ([]string, error) {
	if m == nil || ctx == nil || ctx.Err() != nil || strings.TrimSpace(owner) == "" {
		return nil, fmt.Errorf("local provider inventory unavailable")
	}
	m.localMu.RLock()
	names := []string{}
	for name, registration := range m.local {
		if registration.options != nil && registration.options.PrimitiveProviderIdentity == owner {
			names = append(names, name)
		}
	}
	m.localMu.RUnlock()
	for _, name := range names {
		if err := m.checkLocalCollision(ctx, name); err != nil {
			return nil, err
		}
	}
	return names, nil
}
