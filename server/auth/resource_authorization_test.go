package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"github.com/viant/mcp-protocol/schema"
)

const protectedResourceURI = "datly://localhost/status"
const protectedEscapedResourceURI = "datly://localhost/files/a%2Fb"

func TestEnsureAuthorizedUsesResourceURI(t *testing.T) {
	service, err := New(&Config{Policy: resourcePolicy()})
	if err != nil {
		t.Fatal(err)
	}

	request, err := jsonrpc.NewRequest(schema.MethodResourcesRead, &schema.ReadResourceRequestParams{Uri: protectedResourceURI})
	if err != nil {
		t.Fatal(err)
	}
	response := &jsonrpc.Response{}
	token, err := service.EnsureAuthorized(context.Background(), request, response)
	if err != nil || token != nil || response.Error == nil || response.Error.Code != schema.Unauthorized {
		t.Fatalf("token=%+v error=%v responseError=%+v", token, err, response.Error)
	}

	request, err = jsonrpc.NewRequest(schema.MethodResourcesRead, &schema.ReadResourceRequestParams{Uri: "datly://localhost/public"})
	if err != nil {
		t.Fatal(err)
	}
	response = &jsonrpc.Response{}
	if _, err = service.EnsureAuthorized(context.Background(), request, response); err != nil || response.Error != nil {
		t.Fatalf("public resource error=%v responseError=%+v", err, response.Error)
	}
}

func TestProtectedResourcesHandlerRejectsUnknownResource(t *testing.T) {
	service, err := New(&Config{Policy: resourcePolicy()})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource?resource=datly%3A%2F%2Flocalhost%2Fmissing", nil)
	recorder := httptest.NewRecorder()
	service.ProtectedResourcesHandler(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unknown resource status=%d body=%q", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource?resource=datly%3A%2F%2Flocalhost%2Fstatus", nil)
	recorder = httptest.NewRecorder()
	service.ProtectedResourcesHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("protected resource status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	actual := &meta.ProtectedResourceMetadata{}
	if err := json.Unmarshal(recorder.Body.Bytes(), actual); err != nil {
		t.Fatal(err)
	}
	if actual.Resource != "https://api.example.com" {
		t.Fatalf("metadata=%+v", actual)
	}

	request = httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource?resource="+url.QueryEscape(protectedEscapedResourceURI), nil)
	recorder = httptest.NewRecorder()
	service.ProtectedResourcesHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("escaped resource status=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestMiddlewareEscapesMetadataResourceURIExactlyOnce(t *testing.T) {
	service, err := New(&Config{Policy: resourcePolicy()})
	if err != nil {
		t.Fatal(err)
	}
	rpcRequest, err := jsonrpc.NewRequest(schema.MethodResourcesRead, &schema.ReadResourceRequestParams{Uri: protectedEscapedResourceURI})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(rpcRequest)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://mcp.example/mcp", bytes.NewReader(payload))
	recorder := httptest.NewRecorder()
	service.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unauthorized request reached the MCP handler")
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("authorization status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	challenge := recorder.Header().Get("WWW-Authenticate")
	const metadataPrefix = `Bearer resource_metadata="`
	if !strings.HasPrefix(challenge, metadataPrefix) {
		t.Fatalf("authorization challenge=%q", challenge)
	}
	metadataValue := strings.TrimPrefix(challenge, metadataPrefix)
	end := strings.IndexByte(metadataValue, '"')
	if end < 0 {
		t.Fatalf("authorization challenge=%q", challenge)
	}
	metadataURL := metadataValue[:end]

	request = httptest.NewRequest(http.MethodGet, metadataURL, nil)
	recorder = httptest.NewRecorder()
	service.ProtectedResourcesHandler(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("metadata status=%d URL=%q body=%q", recorder.Code, metadataURL, recorder.Body.String())
	}
	actual := &meta.ProtectedResourceMetadata{}
	if err := json.Unmarshal(recorder.Body.Bytes(), actual); err != nil {
		t.Fatal(err)
	}
	if actual.Resource != "https://api.example.com" {
		t.Fatalf("metadata=%+v", actual)
	}
}

func resourcePolicy() *authorization.Policy {
	rule := &authorization.Authorization{
		RequiredScopes: []string{"read"},
		ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
			Resource: "https://api.example.com",
		},
	}
	return &authorization.Policy{Resources: map[string]*authorization.Authorization{
		protectedResourceURI:        rule,
		protectedEscapedResourceURI: rule,
	}}
}
