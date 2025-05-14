package server

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/client"
	"testing"
)

func TestServerAsClient(t *testing.T) {
	// Create a server with a default implementer
	newImplementer := serverproto.WithDefaultImplementer(context.Background(), func(implementer *serverproto.DefaultImplementer) error {
		// Register a simple resource
		implementer.RegisterResource(schema.Resource{Name: "hello", Uri: "/hello"}, nil)
		return nil
	})

	srv, err := New(
		WithNewImplementer(newImplementer),
		WithImplementation(schema.Implementation{"TestServer", "1.0"}),
		WithCapabilities(schema.ServerCapabilities{Resources: &schema.ServerCapabilitiesResources{}}),
	)
	assert.NoError(t, err)
	assert.NotNil(t, srv)

	// Get a client interface from the server
	ctx := context.Background()
	clientInterface := srv.AsClient(ctx)
	assert.NotNil(t, clientInterface)
	assert.Implements(t, (*client.Interface)(nil), clientInterface)

	// Initialize the client
	result, err := clientInterface.Initialize(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TestServer", result.ServerInfo.Name)
	assert.Equal(t, "1.0", result.ServerInfo.Version)

	// List resources
	resources, err := clientInterface.ListResources(ctx, nil)
	assert.NoError(t, err)
	assert.NotNil(t, resources)
	assert.GreaterOrEqual(t, len(resources.Resources), 1)

	// Verify the resource we registered
	found := false
	for _, resource := range resources.Resources {
		if resource.Name == "hello" && resource.Uri == "/hello" {
			found = true
			break
		}
	}
	assert.True(t, found, "Expected to find the 'hello' resource")
}
