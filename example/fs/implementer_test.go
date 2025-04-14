package fs

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	_ "github.com/viant/afs/embed"
	"github.com/viant/afs/storage"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/mcp/protocol/client"
	"github.com/viant/mcp/protocol/server"
	"github.com/viant/mcp/schema"
	"testing"
	"time"
)

//go:embed testdata/*
var embedFs embed.FS

func TestNew(t *testing.T) {
	go func() {
		err := startServer()
		if err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(time.Second)
	err := runClient(t)
	assert.Nil(t, err)
}

func runClient(t *testing.T) error {
	ctx := context.Background()
	transport, err := getHttpTransport(ctx)
	if err != nil {
		return err
	}
	// Create a new aClient
	aClient := client.New("tester", "0.1", transport, client.WithCapabilities(schema.ClientCapabilities{}))
	initResult, err := aClient.Initialize(ctx)
	if err != nil {
		return err
	}
	assert.Equal(t, "MCP FS", initResult.ServerInfo.Name)
	listResult, jErr := aClient.ListResources(ctx, nil)
	if !assert.Nil(t, jErr) {
		return jErr
	}
	assert.Equal(t, 2, len(listResult.Resources))

	content, rErr := aClient.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: "embed://localhost/testdata/poem.txt"})
	if !assert.Nil(t, rErr) {
		return jErr
	}
	assert.NotNil(t, content)
	return nil
}

func startServer() error {
	config := &Config{
		BaseURL: "embed:///testdata",
		Options: []storage.Option{
			embedFs,
		},
	}
	var newImplementer = New(config)
	var options = []server.Option{
		server.WithNewImplementer(newImplementer),
		server.WithImplementation(schema.Implementation{"MCP FS", "0.1"}),
		server.WithCapabilities(schema.ServerCapabilities{
			Resources: &schema.ServerCapabilitiesResources{},
		}),
	}
	srv, err := server.New(options...)
	if err != nil {
		return err
	}
	ctx := context.Background()
	endpoint := srv.HTTP(ctx, ":4981")
	return endpoint.ListenAndServe()
}

func getHttpTransport(ctx context.Context) (*sse.Client, error) {
	transport, err := sse.New(ctx, "http://localhost:4981/sse",
		sse.WithListener(func(message *jsonrpc.Message) {
			data, err := json.Marshal(message)
			fmt.Printf("data: %v %v %+v\n", string(data), err, message)
		}))
	return transport, err
}
