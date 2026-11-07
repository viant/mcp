package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp-protocol/schema"
	mcpclient "github.com/viant/mcp/client"
	authctx "github.com/viant/mcp/client/auth/link"
)

func capacityManager(t *testing.T, limit int, factory func(context.Context, string, string) (mcpclient.Interface, error)) *Manager {
	t.Helper()
	m, err := New(&concurrencyProviderStub{}, WithMaxClients(limit), WithClientFactory(factory))
	require.NoError(t, err)
	return m
}
func capacityCount(m *Manager) int { m.mu.Lock(); defer m.mu.Unlock(); return m.capacityUsed }
func capacityOwner(subject string) context.Context {
	return authctx.WithUserInfo(context.Background(), &authctx.UserInfo{Subject: subject})
}

func TestCapacityOptionAndDefault(t *testing.T) {
	for _, limit := range []int{0, -1} {
		_, err := New(&concurrencyProviderStub{}, WithMaxClients(limit))
		require.Error(t, err)
	}
	m, err := New(&concurrencyProviderStub{}, WithClientFactory(func(context.Context, string, string) (mcpclient.Interface, error) { return &lifecycleClient{}, nil }))
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		_, err = m.Get(context.Background(), fmt.Sprint(i), "server")
		require.NoError(t, err)
	}
	require.Equal(t, 0, capacityCount(m))
	require.Len(t, m.pool, 20)
}

func TestCapacityReusePartitionsAndLRUEviction(t *testing.T) {
	var created []*lifecycleClient
	m := capacityManager(t, 2, func(context.Context, string, string) (mcpclient.Interface, error) {
		c := &lifecycleClient{}
		created = append(created, c)
		return c, nil
	})
	a, err := m.Get(capacityOwner("alice"), "workspace", "one")
	require.NoError(t, err)
	b, err := m.Get(capacityOwner("bob"), "workspace", "one")
	require.NoError(t, err)
	require.NotSame(t, a, b)
	again, err := m.Get(capacityOwner("alice"), "workspace", "one")
	require.NoError(t, err)
	require.Same(t, a, again)
	require.Len(t, created, 2)
	m.mu.Lock()
	a.(*managedClient).entry.usedAt = time.Unix(2, 0)
	b.(*managedClient).entry.usedAt = time.Unix(1, 0)
	generation := m.toolCatalogGeneration["one"]
	m.mu.Unlock()
	c, err := m.Get(capacityOwner("alice"), "workspace", "two")
	require.NoError(t, err)
	require.NotSame(t, a, c)
	require.EqualValues(t, 1, created[1].closed.Load())
	require.EqualValues(t, 0, created[0].closed.Load())
	require.Equal(t, 2, capacityCount(m))
	require.Greater(t, m.ToolCatalogGeneration("one"), generation)
	_, err = b.CallTool(capacityOwner("bob"), &schema.CallToolRequestParams{})
	require.ErrorContains(t, err, "closed")
	m.CloseConversation("workspace")
	require.Equal(t, 0, capacityCount(m))
	for _, raw := range created {
		require.EqualValues(t, 1, raw.closed.Load())
	}
}

func TestCapacityNeverEvictsActiveRPCAndRetainsRetiredSlot(t *testing.T) {
	raw := &lifecycleClient{started: make(chan struct{}), release: make(chan struct{})}
	var creates atomic.Int32
	m := capacityManager(t, 1, func(context.Context, string, string) (mcpclient.Interface, error) {
		if creates.Add(1) == 1 {
			return raw, nil
		}
		return &lifecycleClient{}, nil
	})
	cli, err := m.Get(context.Background(), "first", "server")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, e := cli.CallTool(context.Background(), &schema.CallToolRequestParams{}); done <- e }()
	<-raw.started
	_, err = m.Get(context.Background(), "other", "server")
	require.ErrorIs(t, err, ErrClientCapacity)
	require.EqualValues(t, 1, creates.Load())
	require.EqualValues(t, 0, raw.closed.Load())
	m.CloseConversation("first")
	require.Equal(t, 1, capacityCount(m))
	_, err = m.Get(context.Background(), "other", "server")
	require.ErrorIs(t, err, ErrClientCapacity)
	close(raw.release)
	require.NoError(t, <-done)
	require.EqualValues(t, 1, raw.closed.Load())
	require.Equal(t, 0, capacityCount(m))
	_, err = m.Get(context.Background(), "other", "server")
	require.NoError(t, err)
	require.Equal(t, 1, capacityCount(m))
	m.CloseConversation("other")
}

func TestCapacityInflightBurstSingleflightAndCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var creates atomic.Int32
	m := capacityManager(t, 1, func(ctx context.Context, _, _ string) (mcpclient.Interface, error) {
		creates.Add(1)
		close(started)
		select {
		case <-release:
			return &lifecycleClient{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	result := make(chan error, 1)
	go func() { _, err := m.Get(context.Background(), "owner", "server"); result <- err }()
	<-started
	const count = 30
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := m.Get(context.Background(), fmt.Sprint(i), "server")
			if !errors.Is(err, ErrClientCapacity) {
				t.Errorf("burst error = %v", err)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 1, creates.Load())
	require.Equal(t, 1, capacityCount(m))
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Get(cancelled, "owner", "server")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, capacityCount(m))
	waiter := make(chan error, 1)
	go func() { _, err := m.Get(context.Background(), "owner", "server"); waiter <- err }()
	close(release)
	require.NoError(t, <-result)
	require.NoError(t, <-waiter)
	require.EqualValues(t, 1, creates.Load())
	m.CloseConversation("owner")
	require.Equal(t, 0, capacityCount(m))
}

func TestCapacityFailureCancellationAndReaperReleaseSlots(t *testing.T) {
	for _, kind := range []string{"failure", "cancel", "nil"} {
		t.Run(kind, func(t *testing.T) {
			started := make(chan struct{})
			raw := &lifecycleClient{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := capacityManager(t, 1, func(ctx context.Context, _, _ string) (mcpclient.Interface, error) {
				close(started)
				switch kind {
				case "failure":
					return raw, errors.New("creation failed")
				case "cancel":
					<-ctx.Done()
					return raw, nil
				default:
					return nil, nil
				}
			})
			done := make(chan error, 1)
			go func() { _, err := m.Get(ctx, "owner", "server"); done <- err }()
			<-started
			if kind == "cancel" {
				cancel()
			}
			require.Error(t, <-done)
			require.Equal(t, 0, capacityCount(m))
			require.Empty(t, m.inflight)
			require.Empty(t, m.pool)
			if kind != "nil" {
				require.EqualValues(t, 1, raw.closed.Load())
			}
			m.newClientFn = func(context.Context, string, string) (mcpclient.Interface, error) { return &lifecycleClient{}, nil }
			_, err := m.Get(context.Background(), "next", "server")
			require.NoError(t, err)
			m.mu.Lock()
			for _, entries := range m.pool {
				for _, entry := range entries {
					entry.usedAt = time.Unix(0, 0)
				}
			}
			m.mu.Unlock()
			m.Reap()
			require.Equal(t, 0, capacityCount(m))
			require.Empty(t, m.pool)
		})
	}
}

func TestCapacityCloseDuringCreationReleasesReservation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	raw := &lifecycleClient{}
	m := capacityManager(t, 1, func(context.Context, string, string) (mcpclient.Interface, error) {
		close(started)
		<-release
		return raw, nil
	})
	done := make(chan error, 1)
	go func() { _, err := m.Get(context.Background(), "owner", "server"); done <- err }()
	<-started
	m.CloseConversation("owner")
	require.Equal(t, 1, capacityCount(m))
	_, err := m.Get(context.Background(), "new", "server")
	require.ErrorIs(t, err, ErrClientCapacity)
	close(release)
	require.ErrorContains(t, <-done, "discarded")
	require.EqualValues(t, 1, raw.closed.Load())
	require.Equal(t, 0, capacityCount(m))
}

type capacitySlowCloseClient struct {
	lifecycleClient
	closing chan struct{}
	finish  chan struct{}
}

func (c *capacitySlowCloseClient) Close() error {
	close(c.closing)
	<-c.finish
	return c.lifecycleClient.Close()
}
func TestCapacityCountsClosingTransports(t *testing.T) {
	for _, mode := range []string{"LRU", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			raw := &capacitySlowCloseClient{closing: make(chan struct{}), finish: make(chan struct{})}
			var creates atomic.Int32
			m := capacityManager(t, 1, func(context.Context, string, string) (mcpclient.Interface, error) {
				if creates.Add(1) == 1 {
					return raw, nil
				}
				return &lifecycleClient{}, nil
			})
			_, err := m.Get(context.Background(), "first", "server")
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() {
				if mode == "LRU" {
					_, err := m.Get(context.Background(), "next", "server")
					done <- err
				} else {
					m.CloseConversation("first")
					done <- nil
				}
			}()
			<-raw.closing
			require.Equal(t, 1, capacityCount(m))
			_, err = m.Get(context.Background(), "burst", "server")
			require.ErrorIs(t, err, ErrClientCapacity)
			require.EqualValues(t, 1, creates.Load())
			close(raw.finish)
			require.NoError(t, <-done)
			if mode == "LRU" {
				require.EqualValues(t, 2, creates.Load())
				m.CloseConversation("next")
			}
			require.Equal(t, 0, capacityCount(m))
			require.EqualValues(t, 1, raw.closed.Load())
		})
	}
}

func TestCapacityCredentialEvictionAndReconnectAccounting(t *testing.T) {
	var created []*lifecycleClient
	m := capacityManager(t, 2, func(context.Context, string, string) (mcpclient.Interface, error) {
		raw := &lifecycleClient{}
		created = append(created, raw)
		return raw, nil
	})
	alice, bob := capacityOwner("alice"), capacityOwner("bob")
	_, err := m.Get(alice, "workspace", "server")
	require.NoError(t, err)
	before, err := m.Get(bob, "workspace", "server")
	require.NoError(t, err)
	m.EvictUserServer("alice", "server")
	require.Equal(t, 1, capacityCount(m))
	require.EqualValues(t, 1, created[0].closed.Load())
	require.EqualValues(t, 0, created[1].closed.Load())
	after, err := m.Reconnect(bob, "workspace", "server")
	require.NoError(t, err)
	require.NotSame(t, before, after)
	require.Equal(t, 1, capacityCount(m))
	require.EqualValues(t, 1, created[1].closed.Load())
	_, err = m.Get(alice, "workspace", "server")
	require.NoError(t, err)
	require.Equal(t, 2, capacityCount(m))
	m.CloseConversation("workspace")
	require.Equal(t, 0, capacityCount(m))
	for _, raw := range created {
		require.EqualValues(t, 1, raw.closed.Load())
	}
}
