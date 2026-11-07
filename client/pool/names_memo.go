// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"net/http"
	"sync"
)

// namesMemo is scoped to one inbound operation. It avoids rebuilding the
// dynamic MCP connection inventory for each nested primitive lookup while
// preserving a fresh owner-supplied list for the next operation.
type namesMemo struct {
	mu     sync.Mutex
	values map[namesMemoEntry][]string
}

type namesMemoContextKey struct{}
type namesMemoEntry struct {
	manager           *Manager
	scope, generation string
}

// WithNamesMemo establishes a detached, request-local inventory snapshot.
// The manager still checks effective actor and credential scope on each read.
func WithNamesMemo(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, namesMemoContextKey{}, &namesMemo{values: map[namesMemoEntry][]string{}})
}

func namesMemoFromContext(ctx context.Context) *namesMemo {
	if ctx == nil {
		return nil
	}
	memo, _ := ctx.Value(namesMemoContextKey{}).(*namesMemo)
	return memo
}

func (memo *namesMemo) get(manager *Manager, scope, generation string) ([]string, bool) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	value, ok := memo.values[namesMemoEntry{manager: manager, scope: scope, generation: generation}]
	return append([]string(nil), value...), ok
}

func (memo *namesMemo) put(manager *Manager, scope, generation string, value []string) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	memo.values[namesMemoEntry{manager: manager, scope: scope, generation: generation}] = append([]string(nil), value...)
}

// WithRequestCatalog shares one operation's catalog preparation without
// persisting selected resource definitions or execution results.
func WithRequestCatalog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithToolCatalogReadSnapshot(WithNamesMemo(r.Context()))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
