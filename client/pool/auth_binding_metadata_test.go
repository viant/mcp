package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExplicitMetadataPathAndCacheIsolation(t *testing.T) {
	var origin string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resource := origin
		if r.URL.Path == "/.well-known/oauth-protected-resource/v2" {
			resource += "/v2/mcp"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"resource": resource, "authorization_servers": []string{"https://issuer.example"}})
	}))
	defer server.Close()
	origin = server.URL
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = previous }()
	m := &Manager{}
	ctx := context.Background()
	resource := origin + "/v2/mcp"
	if err := m.crossCheckExplicitMetadata(ctx, t.Name(), resource, "https://issuer.example", resource, ""); err == nil {
		t.Fatal("root resource mismatch accepted")
	}
	metadata := origin + "/.well-known/oauth-protected-resource/v2"
	if err := m.crossCheckExplicitMetadata(ctx, t.Name(), resource, "https://issuer.example", resource, metadata); err != nil {
		t.Fatal(err)
	}
	if err := m.crossCheckExplicitMetadata(ctx, t.Name(), resource, "https://other.example", resource, metadata); err == nil {
		t.Fatal("cached result hid issuer change")
	}
	if err := m.crossCheckExplicitMetadata(ctx, t.Name(), resource, "https://issuer.example", origin+"/V2/mcp", metadata); err == nil {
		t.Fatal("resource path case mismatch accepted")
	}
	if err := m.crossCheckExplicitMetadata(ctx, t.Name(), resource, "https://issuer.example", resource, "https://other.example/metadata"); err == nil {
		t.Fatal("cross-origin metadata accepted")
	}
}
