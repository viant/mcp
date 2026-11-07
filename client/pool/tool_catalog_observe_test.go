package manager

import (
	"context"
	"github.com/stretchr/testify/require"
	mcpclient "github.com/viant/mcp/client"
	"github.com/viant/mcp/client/observe"
	"testing"
)

func TestToolCatalogObservationSeparatesSourceFromCachedReads(t *testing.T) {
	_, _, client, managed, _ := catalogSetup(t, false)
	var events []observe.Event
	ctx := observe.WithObserver(context.Background(), observe.ObserverFunc(func(e observe.Event) { events = append(events, e) }))
	for range 2 {
		_, err := managed.ListTools(ctx, nil)
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, client.calls.Load())
	_, err := managed.ListTools(ctx, nil, mcpclient.WithNoRetry())
	require.NoError(t, err)
	var source []observe.Event
	for _, event := range events {
		if event.Phase == "mcp.tools.list.source" {
			source = append(source, event)
		}
	}
	require.Len(t, source, 2, "a cache hit must not be counted as a source request")
	require.Equal(t, observe.CacheMiss, source[0].Cache)
	require.Equal(t, observe.CacheBypass, source[1].Cache)
	require.Equal(t, "server", source[0].Provider)
}
