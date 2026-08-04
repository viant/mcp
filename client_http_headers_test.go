package mcp

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/viant/mcp-protocol/schema"
)

type recordingRoundTripper struct {
	request *http.Request
	body    string
}

func (r *recordingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	r.request = request
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	r.body = string(body)
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func TestContextAuthHeaderTransport_AddsJulyStandardHeaders(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"file:///report","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	request, err := http.NewRequest(http.MethodPost, "http://example.test/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(schema.HeaderProtocolVersion, schema.LatestProtocolVersion)
	recorder := &recordingRoundTripper{}
	transport := &contextAuthHeaderTransport{base: recorder}

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if actual := recorder.request.Header.Get(schema.HeaderMethod); actual != schema.MethodResourcesRead {
		t.Fatalf("expected method header %q, got %q", schema.MethodResourcesRead, actual)
	}
	if actual := recorder.request.Header.Get(schema.HeaderName); actual != "file:///report" {
		t.Fatalf("expected name header %q, got %q", "file:///report", actual)
	}
	if recorder.body != body {
		t.Fatalf("request body changed: %s", recorder.body)
	}
}

func TestContextAuthHeaderTransport_DoesNotAddRoutingHeadersToNotification(t *testing.T) {
	body := `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{}}`
	request, err := http.NewRequest(http.MethodPost, "http://example.test/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(schema.HeaderProtocolVersion, schema.LatestProtocolVersion)
	recorder := &recordingRoundTripper{}
	transport := &contextAuthHeaderTransport{base: recorder}

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if actual := recorder.request.Header.Get(schema.HeaderMethod); actual != "" {
		t.Fatalf("unexpected notification method header %q", actual)
	}
}

func TestContextAuthHeaderTransport_AllowsStatelessJSONRPCResponse(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":9,"result":{"accepted":true}}`
	request, err := http.NewRequest(http.MethodPost, "http://example.test/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(schema.HeaderProtocolVersion, schema.LatestProtocolVersion)
	recorder := &recordingRoundTripper{}
	transport := &contextAuthHeaderTransport{base: recorder}

	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	if actual := recorder.request.Header.Get(schema.HeaderMethod); actual != "" {
		t.Fatalf("unexpected response method header %q", actual)
	}
}
