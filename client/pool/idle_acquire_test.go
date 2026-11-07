package manager

import (
	"context"
	"github.com/stretchr/testify/require"
	mcpclient "github.com/viant/mcp/client"
	"testing"
	"time"
)

func TestGetReplacesExpiredIdleClientBeforeReaperWithoutCrossingUsers(t *testing.T) {
	type userKey struct{}
	m, err := New(&concurrencyProviderStub{}, WithTTL(time.Minute), WithUserIDExtractor(func(ctx context.Context) string { return ctx.Value(userKey{}).(string) }))
	require.NoError(t, err)
	var created []*lifecycleClient
	m.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) {
		c := &lifecycleClient{}
		created = append(created, c)
		return c, nil
	}
	alice := context.WithValue(context.Background(), userKey{}, "alice")
	bob := context.WithValue(context.Background(), userKey{}, "bob")
	first, err := m.Get(alice, "catalog", "primary")
	require.NoError(t, err)
	other, err := m.Get(bob, "catalog", "primary")
	require.NoError(t, err)
	m.mu.Lock()
	m.pool[m.poolKey(alice, "catalog")]["primary"].usedAt = time.Now().Add(-2 * time.Minute)
	m.mu.Unlock()
	m.mu.Lock()
	m.pool[m.poolKey(alice, "catalog")]["primary"].active = 1
	m.mu.Unlock()
	busy, err := m.Get(alice, "catalog", "primary")
	require.NoError(t, err)
	require.Same(t, first, busy, "an active operation must not be interrupted")
	require.Zero(t, created[0].closed.Load())
	m.mu.Lock()
	m.pool[m.poolKey(alice, "catalog")]["primary"].active = 0
	m.pool[m.poolKey(alice, "catalog")]["primary"].usedAt = time.Now().Add(-2 * time.Minute)
	m.mu.Unlock()
	next, err := m.Get(alice, "catalog", "primary")
	require.NoError(t, err)
	require.NotSame(t, first, next, "Get must not revive an expired idle session")
	again, err := m.Get(bob, "catalog", "primary")
	require.NoError(t, err)
	require.Same(t, other, again, "one user's reconnect must preserve the other user's connection")
	require.Len(t, created, 3)
	waitClosed(t, created[0], 1)
	require.Zero(t, created[1].closed.Load())
	m.CloseConversation("catalog")
}
