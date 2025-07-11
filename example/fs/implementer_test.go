package fs

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	serverproto "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/example/resource"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	_ "github.com/viant/afs/embed"
	"github.com/viant/afs/storage"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport/client/http/sse"
	"github.com/viant/mcp-protocol/schema"
	"github.com/viant/mcp/client"
	"github.com/viant/mcp/server"
)

//go:embed testdata/*
var embedFs embed.FS

func TestFSExample(t *testing.T) {
	go func() {
		if err := startServer(); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(time.Second)
	if err := runClient(t); err != nil {
		t.Error(err)
	}
}

func startServer() error {
	config := &resource.Config{
		BaseURL: "embed:///testdata",
		Options: []storage.Option{embedFs},
	}

	resources := resource.NewFileSystem(config)

	NewServer := serverproto.WithDefaultHandler(context.Background(), func(server *serverproto.DefaultHandler) error {
		assets, _ := resources.Resources(context.Background())
		for _, asset := range assets {
			server.RegisterResource(asset.Metadata, asset.Handler)
		}
		return nil
	})
	srv, err := server.New(
		server.WithNewHandler(NewServer),
		server.WithImplementation(schema.Implementation{Name: "FS", Version: "0.1"}),
	)

	if err != nil {
		return err
	}
	endpoint := srv.HTTP(context.Background(), ":4982")
	return endpoint.ListenAndServe()
}

func runClient(t *testing.T) error {
	ctx := context.Background()
	transport, err := sse.New(ctx, "http://localhost:4982/sse", sse.WithListener(func(msg *jsonrpc.Message) {
		data, _ := json.Marshal(msg)
		fmt.Println(string(data))
	}))
	if err != nil {
		return err
	}
	aClient := client.New("tester", "0.1", transport, client.WithCapabilities(schema.ClientCapabilities{}))
	initRes, err := aClient.Initialize(ctx)
	assert.Nil(t, err)
	assert.Equal(t, "FS", initRes.ServerInfo.Name)
	listRes, err := aClient.ListResources(ctx, nil)
	assert.Nil(t, err)
	assert.GreaterOrEqual(t, len(listRes.Resources), 1)

	content, err := aClient.ReadResource(ctx, &schema.ReadResourceRequestParams{Uri: "embed://localhost/testdata/poem.txt"})
	assert.Nil(t, err)
	assert.NotNil(t, content)

	return nil
}
