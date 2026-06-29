package mcp

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	authtransport "github.com/viant/mcp/client/auth/transport"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestWrapContextAuthHTTPClient_InjectsBearerHeaderFromContext(t *testing.T) {
	var seenAuth string
	client := wrapContextAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	})

	req, err := http.NewRequestWithContext(
		context.WithValue(context.Background(), authtransport.ContextAuthTokenKey, "token-123"),
		http.MethodGet,
		"http://example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}

	if _, err = client.Do(req); err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	if seenAuth != "Bearer token-123" {
		t.Fatalf("Authorization = %q, want %q", seenAuth, "Bearer token-123")
	}
}

func TestWrapContextAuthHTTPClient_PreservesExistingAuthorizationHeader(t *testing.T) {
	var seenAuth string
	client := wrapContextAuthHTTPClient(&http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seenAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	})

	req, err := http.NewRequestWithContext(
		context.WithValue(context.Background(), authtransport.ContextAuthTokenKey, "token-123"),
		http.MethodGet,
		"http://example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer existing")

	if _, err = client.Do(req); err != nil {
		t.Fatalf("client.Do() error = %v", err)
	}
	if seenAuth != "Bearer existing" {
		t.Fatalf("Authorization = %q, want %q", seenAuth, "Bearer existing")
	}
}
