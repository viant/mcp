package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	authcfg "github.com/viant/mcp/client/auth/config"
)

func TestNoRetryHTTPDoesNotReplayRedirectedEffect(t *testing.T) {
	var first, second atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/first" {
			first.Add(1)
			w.Header().Set("Location", "/second")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		second.Add(1)
		_, _ = io.WriteString(w, "done")
	}))
	defer server.Close()
	client := wrapContextAuthHTTPClient(server.Client())
	for _, noRetry := range []bool{true, false} {
		ctx := context.Background()
		if noRetry {
			ctx = authcfg.WithNoRetry(ctx)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/first", strings.NewReader("effect"))
		require.NoError(t, err)
		response, err := client.Do(request)
		require.NoError(t, err)
		_ = response.Body.Close()
		if noRetry {
			require.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
			require.Zero(t, second.Load())
		} else {
			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, int32(1), second.Load())
		}
	}
	require.Equal(t, int32(2), first.Load())
}
