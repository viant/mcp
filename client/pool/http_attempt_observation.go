// SPDX-License-Identifier: Apache-2.0
package manager

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
)

// HTTPAttempt contains only bounded metadata from the response returned by the
// configured auth transport. Retries internal to that transport are opaque.
type HTTPAttempt struct {
	ServerName     string
	RequestSent    bool
	StatusCode     int
	RequestID      string
	TransportError bool
}

type httpAttemptObserverKey struct{}

// WithHTTPAttemptObserver adds a request-scoped metadata observer. It does not
// retain URLs, headers, credentials, or request and response bodies.
func WithHTTPAttemptObserver(ctx context.Context, observer func(HTTPAttempt)) context.Context {
	if ctx == nil || observer == nil {
		return ctx
	}
	return context.WithValue(ctx, httpAttemptObserverKey{}, observer)
}

type observingRoundTripper struct {
	base       http.RoundTripper
	serverName string
}

func (t observingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	observer, _ := req.Context().Value(httpAttemptObserverKey{}).(func(HTTPAttempt))
	if observer == nil {
		return base.RoundTrip(req)
	}
	var sent atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			sent.Store(true)
		}
	}}
	observed := req.Clone(httptrace.WithClientTrace(req.Context(), trace))
	response, err := base.RoundTrip(observed)
	attempt := HTTPAttempt{ServerName: t.serverName, RequestSent: sent.Load()}
	if response != nil && attempt.RequestSent {
		attempt.StatusCode = response.StatusCode
		attempt.RequestID = safeRequestID(response.Header.Get("X-Request-ID"))
	}
	var netErr net.Error
	attempt.TransportError = err != nil && errors.As(err, &netErr)
	observer(attempt)
	return response, err
}

func safeRequestID(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return ""
	}
	return value
}
