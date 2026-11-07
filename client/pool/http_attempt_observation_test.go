package manager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	authtransport "github.com/viant/mcp/client/auth/transport"
)

func TestHTTPAttemptRecordsOnlySafeRemoteMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "request-123")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "private-response")
	}))
	defer server.Close()
	var attempts []HTTPAttempt
	ctx := WithHTTPAttemptObserver(context.Background(), func(attempt HTTPAttempt) { attempts = append(attempts, attempt) })
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader("private-request"))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer private-token")
	response, err := (observingRoundTripper{base: http.DefaultTransport, serverName: "example"}).RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, []HTTPAttempt{{ServerName: "example", RequestSent: true, StatusCode: http.StatusForbidden, RequestID: "request-123"}}, attempts)
	raw, err := json.Marshal(attempts[0])
	require.NoError(t, err)
	for _, secret := range []string{"private-token", "private-request", "private-response", server.URL} {
		require.NotContains(t, string(raw), secret)
	}
}

type syntheticForbiddenTransport struct{}

func (syntheticForbiddenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusForbidden, Header: http.Header{"X-Request-Id": {"synthetic"}}, Body: io.NopCloser(strings.NewReader("private"))}, nil
}

func TestHTTPAttemptDoesNotCallSyntheticResponseRemote(t *testing.T) {
	var observed HTTPAttempt
	ctx := WithHTTPAttemptObserver(context.Background(), func(attempt HTTPAttempt) { observed = attempt })
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid", nil)
	require.NoError(t, err)
	response, err := (observingRoundTripper{base: syntheticForbiddenTransport{}, serverName: "example"}).RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.False(t, observed.RequestSent)
	require.Zero(t, observed.StatusCode)
	require.Empty(t, observed.RequestID)
}

func TestHTTPAttemptSurvivesAuthTransportRequestClone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	authRT, err := authtransport.New(authtransport.WithTransport(http.DefaultTransport))
	require.NoError(t, err)
	var observed HTTPAttempt
	ctx := WithHTTPAttemptObserver(context.Background(), func(attempt HTTPAttempt) { observed = attempt })
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, nil)
	require.NoError(t, err)
	response, err := (observingRoundTripper{base: authRT, serverName: "example"}).RoundTrip(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusForbidden, observed.StatusCode)
	require.True(t, observed.RequestSent)
}

func TestSafeRequestIDRejectsUntrustedHeaderValues(t *testing.T) {
	require.Empty(t, safeRequestID("private token"))
	require.Empty(t, safeRequestID(strings.Repeat("a", 129)))
	require.Empty(t, safeRequestID("request\nAuthorization:secret"))
	require.Equal(t, "request-123:part_2", safeRequestID("request-123:part_2"))
}
