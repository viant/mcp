package manager

import (
	"context"
	"github.com/stretchr/testify/require"
	mcpclient "github.com/viant/mcp/client"
	"testing"
	"time"
)

func TestClientEvictionNotificationRunsOutsideLockAndUnsubscribes(t *testing.T) {
	m := capacityManager(t, 2, func(context.Context, string, string) (mcpclient.Interface, error) { return &lifecycleClient{}, nil })
	ctx := capacityOwner("owner")
	held, err := m.Get(ctx, "conversation", "one")
	require.NoError(t, err)
	events := make(chan ClientEviction, 2)
	stop := m.OnClientEvicted(func(event ClientEviction) {
		// Reentrant manager read would deadlock if the callback held m.mu.
		require.False(t, m.IsCurrentClient(ctx, "conversation", "one", held))
		m.ToolCatalogGeneration(event.Server)
		events <- event
	})
	m.mu.Lock()
	held.(*managedClient).entry.usedAt = time.Time{}
	m.mu.Unlock()
	m.Reap()
	var event ClientEviction
	select {
	case event = <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("eviction notification not delivered")
	}
	require.Equal(t, "one", event.Server)
	require.Same(t, held, event.Client)
	require.Equal(t, ClientIdleEvicted, event.Reason)
	stop()
	_, err = m.Get(ctx, "conversation", "one")
	require.NoError(t, err)
	m.CloseConversation("conversation")
	select {
	case <-events:
		t.Fatal("unsubscribed listener received retirement")
	default:
	}
}
