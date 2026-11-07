// SPDX-License-Identifier: Apache-2.0
package manager

import (
	"context"
	"errors"
	"fmt"
)

// NamesExcluding preserves the native connection inventory while removing
// separately supplied metadata owners before inspecting connection options.
// Unlike CatalogNamesExcluding it includes ordinary published tool connections.
func (m *Manager) NamesExcluding(ctx context.Context, excluded []string) ([]string, error) {
	if m == nil || ctx == nil || ctx.Err() != nil {
		return nil, errors.New("mcp native inventory unavailable")
	}
	var names []string
	if lister, ok := m.prov.(providerLister); ok {
		var err error
		names, err = lister.Names(ctx)
		if err != nil {
			return nil, err
		}
	}
	skip := map[string]bool{}
	for _, name := range excluded {
		skip[name] = true
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		if m.IsLocalClient(name) {
			return nil, fmt.Errorf("mcp local connection collides with configured provider %q", name)
		}
		if !skip[name] {
			filtered = append(filtered, name)
		}
	}
	return m.mergeInventoryNames(ctx, filtered)
}
