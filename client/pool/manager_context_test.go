package manager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/viant/mcp"
	mcpschema "github.com/viant/mcp-protocol/schema"
	mcpcfg "github.com/viant/mcp/client/config"
)

type contextBoundProvider struct {
	url string
}

func (p *contextBoundProvider) Options(context.Context, string) (*mcpcfg.MCPClient, error) {
	return &mcpcfg.MCPClient{ClientOptions: &mcp.ClientOptions{
		ProtocolVersion: mcpschema.LatestProtocolVersion,
		Transport: mcp.ClientTransport{
			Type:                "streamable",
			ClientTransportHTTP: mcp.ClientTransportHTTP{URL: p.url},
		},
	}}, nil
}

func TestNewClientHonorsDiscoveryContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			http.Error(w, "late response", http.StatusGatewayTimeout)
		}
	}))
	defer server.Close()

	mgr, err := New(&contextBoundProvider{url: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err = mgr.newClient(ctx, "conversation-1", "blocked"); err == nil {
		t.Fatal("newClient() error = nil, want deadline error")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("newClient() ignored caller deadline: elapsed=%s error=%v", elapsed, err)
	}
}
