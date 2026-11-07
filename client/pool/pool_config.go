// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	cfg "github.com/viant/mcp/client/config"
)

// Names can be configured as URLs; never echo their credentials or query.
func poolConfigLogName(name string) string {
	parsed, err := url.Parse(name)
	if err != nil || len(name) > 512 {
		key := sha256.Sum256([]byte(name))
		return fmt.Sprintf("source:%x", key[:6])
	}
	parsed.User, parsed.RawQuery, parsed.Fragment = nil, "", ""
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		parsed.Path, parsed.RawPath = "", ""
	}
	return parsed.String()
}

func effectiveConfigFingerprint(options *cfg.MCPClient) ([32]byte, error) {
	if options == nil {
		return [32]byte{}, fmt.Errorf("MCP definition unavailable")
	}
	raw, err := json.Marshal(options)
	if err != nil {
		return [32]byte{}, err
	}
	var normalized cfg.MCPClient
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&normalized); err != nil {
		return [32]byte{}, err
	}
	if normalized.ClientOptions != nil {
		normalized.Init()
		if normalized.Auth != nil && !normalized.IsDelegatedAuth() && normalized.Auth.BackendForFrontend == nil {
			enabled := true
			normalized.Auth.BackendForFrontend = &enabled
		}
	}
	raw, err = json.Marshal([]any{&normalized, options.ResolvedIdentity})
	return sha256.Sum256(raw), err
}

func (m *Manager) evictConfiguredClient(ctx context.Context, conversation, name string) {
	key := m.poolKey(ctx, conversation)
	m.mu.Lock()
	resolved := name
	if m.pool[key][resolved] == nil {
		for service, entry := range m.pool[key] {
			if entry.aliases[name] {
				resolved = service
				break
			}
		}
	}
	client := m.evictEntryLocked(key, resolved, m.pool[key][resolved])
	m.mu.Unlock()
	closeClientBestEffort(client)
}

// BindDefinitionReader is startup-only and reserves already registered local
// connection names before exposing the reader. Custom providers are not reset.
type DefinitionReader interface {
	List(context.Context) ([]string, error)
	Load(context.Context, string) (*cfg.MCPClient, error)
}

func (m *Manager) BindDefinitionReader(reader DefinitionReader) (bool, error) {
	if m == nil {
		return false, fmt.Errorf("MCP manager unavailable")
	}
	m.definitionBindMu.Lock()
	defer m.definitionBindMu.Unlock()
	provider, ok := m.prov.(interface {
		BindDefinitionReader(DefinitionReader, []string) error
	})
	if !ok {
		return false, nil
	}
	m.localMu.RLock()
	names := make([]string, 0, len(m.local))
	for name := range m.local {
		names = append(names, name)
	}
	m.localMu.RUnlock()
	sort.Strings(names)
	err := provider.BindDefinitionReader(reader, names)
	return err == nil, err
}

func resolvedServiceName(options *cfg.MCPClient, fallback string) string {
	if options != nil && options.ResolvedIdentity != nil && options.ResolvedIdentity.ServiceName != "" {
		return options.ResolvedIdentity.ServiceName
	}
	return fallback
}
