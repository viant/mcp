package manager

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
	cfg "github.com/viant/mcp/client/config"
)

func TestIsCurrentClientPinsExactOwnerSessionAndDoesNotRenew(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "registered-local"}[local], func(t *testing.T) {
			inventory := &localInventoryProvider{names: []string{"external"}}
			constructions := 0
			mgr, err := New(inventory, WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
				constructions++
				return &stubClient{}, nil
			}))
			require.NoError(t, err)
			defer mgr.CloseConversation("conv")
			name := "external"
			if local {
				name = "internal"
				require.NoError(t, mgr.RegisterLocal(context.Background(), name, &cfg.MCPClient{ClientOptions: &mcp.ClientOptions{}, PrimitiveProviderIdentity: "owned-local"}, func(context.Context) (mcpclient.Interface, error) { constructions++; return &stubClient{}, nil }))
			}
			alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
			first, err := mgr.Get(alice, "conv", name)
			require.NoError(t, err)
			managed := first.(*managedClient)
			heldTime := managed.entry.usedAt
			for i := 0; i < 3; i++ {
				require.True(t, mgr.IsCurrentClient(alice, "conv", name, first))
			}
			require.Equal(t, heldTime, managed.entry.usedAt, "validation cannot renew the pooled session")
			require.Equal(t, 1, constructions, "validation cannot create a replacement")
			require.False(t, mgr.IsCurrentClient(bob, "conv", name, first))
			require.False(t, mgr.IsCurrentClient(alice, "other", name, first))
			require.False(t, mgr.IsCurrentClient(alice, "conv", "other", first))
			require.False(t, mgr.IsCurrentClient(alice, "conv", name, &stubClient{}))
			cancelled, cancel := context.WithCancel(alice)
			cancel()
			require.False(t, mgr.IsCurrentClient(cancelled, "conv", name, first))
			mgr.CloseConversation("conv")
			require.False(t, mgr.IsCurrentClient(alice, "conv", name, first))
			replacement, err := mgr.Get(alice, "conv", name)
			require.NoError(t, err)
			require.False(t, mgr.IsCurrentClient(alice, "conv", name, first))
			require.True(t, mgr.IsCurrentClient(alice, "conv", name, replacement))
			mgr.EvictUserServer("alice", name)
			require.False(t, mgr.IsCurrentClient(alice, "conv", name, replacement))
			third, err := mgr.Get(alice, "conv", name)
			require.NoError(t, err)
			require.True(t, mgr.IsCurrentClient(alice, "conv", name, third))
			mgr.mu.Lock()
			third.(*managedClient).entry.usedAt = time.Now().Add(-time.Hour)
			mgr.ttl = time.Second
			mgr.mu.Unlock()
			mgr.Reap()
			require.False(t, mgr.IsCurrentClient(alice, "conv", name, third))
		})
	}
}

func TestEvictCurrentClientFencesStaleFailuresAndRetiresActiveCalls(t *testing.T) {
	var made []*lifecycleClient
	mgr, err := New(&concurrencyProviderStub{}, WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) {
		client := &lifecycleClient{}
		if len(made) == 0 {
			client.started = make(chan struct{})
			client.release = make(chan struct{})
		}
		made = append(made, client)
		return client, nil
	}))
	require.NoError(t, err)
	defer mgr.CloseConversation("conv")
	alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
	bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
	first, err := mgr.Get(alice, "conv", "server")
	require.NoError(t, err)
	foreign, err := mgr.Get(bob, "conv", "server")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := first.CallTool(alice, nil)
		done <- err
	}()
	select {
	case <-made[0].started:
	case <-time.After(time.Second):
		t.Fatal("business call did not start")
	}
	require.False(t, mgr.EvictCurrentClient(bob, "conv", "server", first), "other actor cannot retire a client")
	require.True(t, mgr.EvictCurrentClient(alice, "conv", "server", first))
	require.Equal(t, int32(0), made[0].closed.Load(), "active call must retain transport")
	require.True(t, mgr.IsCurrentClient(bob, "conv", "server", foreign))
	replacement, err := mgr.Get(alice, "conv", "server")
	require.NoError(t, err)
	require.NotSame(t, first, replacement)
	require.False(t, mgr.EvictCurrentClient(alice, "conv", "server", first), "late failure cannot retire newer client")
	require.True(t, mgr.IsCurrentClient(alice, "conv", "server", replacement))
	close(made[0].release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("business call did not finish")
	}
	waitClosed(t, made[0], 1)
}

func TestIsCurrentClientPreservesUnrelatedServerAndCanonicalPrincipalPools(t *testing.T) {
	for _, customOwner := range []bool{false, true} {
		t.Run(map[bool]string{false: "canonical-principal", true: "configured-owner"}[customOwner], func(t *testing.T) {
			options := []Option{WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return &stubClient{}, nil })}
			if customOwner {
				options = append(options, WithUserIDExtractor(authctx.EffectiveUserID))
			}
			mgr, err := New(&concurrencyProviderStub{}, options...)
			require.NoError(t, err)
			defer mgr.CloseConversation("conv")
			alice := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "alice"})
			bob := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: "bob"})
			first, err := mgr.Get(alice, "conv", "first")
			require.NoError(t, err)
			second, err := mgr.Get(alice, "conv", "second")
			require.NoError(t, err)
			require.True(t, mgr.IsCurrentClient(alice, "conv", "first", first))
			require.False(t, mgr.IsCurrentClient(bob, "conv", "first", first))
			// Creation epochs fence inflight work; evicting another server does not
			// evict this existing entry or cause normal Get to replace its session.
			held := second.(*managedClient)
			mgr.mu.Lock()
			closed := mgr.evictEntryLocked(held.key, "second", held.entry)
			mgr.epoch[held.key]++
			mgr.mu.Unlock()
			closeClientBestEffort(closed)
			require.False(t, mgr.IsCurrentClient(alice, "conv", "second", second))
			require.True(t, mgr.IsCurrentClient(alice, "conv", "first", first))
			current, err := mgr.Get(alice, "conv", "first")
			require.NoError(t, err)
			require.Same(t, first, current)
			require.True(t, mgr.IsCurrentClient(alice, "conv", "first", current))
		})
	}
}

func TestEvictUserServerMatchesExactCanonicalPrincipalWithColonSubjects(t *testing.T) {
	for _, subject := range []string{"alice", "alice:team"} {
		t.Run(subject, func(t *testing.T) {
			mgr, err := New(&concurrencyProviderStub{}, WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return &stubClient{}, nil }))
			require.NoError(t, err)
			defer mgr.CloseConversation("conv")
			ctx := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: subject})
			foreign := authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: subject + ":foreign"})
			mine, err := mgr.Get(ctx, "conv", "server")
			require.NoError(t, err)
			theirs, err := mgr.Get(foreign, "conv", "server")
			require.NoError(t, err)
			other, err := mgr.Get(ctx, "conv", "other-server")
			require.NoError(t, err)
			mgr.EvictUserServer(subject, "server")
			require.False(t, mgr.IsCurrentClient(ctx, "conv", "server", mine), "disconnect must evict the default authenticated pool")
			require.True(t, mgr.IsCurrentClient(foreign, "conv", "server", theirs), "length-delimited principal owner must reject foreign prefix matches")
			require.True(t, mgr.IsCurrentClient(ctx, "conv", "other-server", other))
			fresh, err := mgr.Get(ctx, "conv", "server")
			require.NoError(t, err)
			require.True(t, mgr.IsCurrentClient(ctx, "conv", "server", fresh))
			require.False(t, mgr.IsCurrentClient(ctx, "conv", "server", mine))
		})
	}
}
