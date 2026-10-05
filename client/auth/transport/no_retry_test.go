package transport

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/mcp/client/auth/config"
)

func TestNoRetryPreventsDelegatedRefreshAndChallengeReplay(t *testing.T) {
	for _, challenge := range []bool{false, true} {
		t.Run(map[bool]string{false: "eager", true: "challenge"}[challenge], func(t *testing.T) {
			resolver := &fakeResolver{current: "stale", refreshed: "good"}
			inner := &countingTransport{accepted: "good"}
			rt := newDelegatedRoundTripper(t, resolver, inner)
			if challenge {
				rt = newChallengeRoundTripper(t, resolver, inner)
			}
			req, err := http.NewRequestWithContext(config.WithNoRetry(context.Background()), http.MethodPost, "https://example.test/mcp", nil)
			require.NoError(t, err)
			response, err := rt.RoundTrip(req)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusUnauthorized, response.StatusCode)
			require.Len(t, inner.requests, 1)
			require.Zero(t, resolver.refreshCount)
			if challenge {
				require.Zero(t, resolver.resolveCount)
			}
		})
	}
}
func TestNoRetryPreventsLegacyAuthenticationReplay(t *testing.T) {
	inner := &countingTransport{accepted: "good"}
	rt := &RoundTripper{transport: inner}
	req, err := http.NewRequestWithContext(config.WithNoRetry(context.Background()), http.MethodPost, "https://example.test/mcp", nil)
	require.NoError(t, err)
	response, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.Len(t, inner.requests, 1)
}
