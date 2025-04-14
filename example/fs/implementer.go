package fs

import (
	"context"
	"encoding/base64"
	"github.com/viant/afs"
	"github.com/viant/afs/storage"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	"github.com/viant/mcp/implementer"
	"github.com/viant/mcp/logger"
	"github.com/viant/mcp/protocol/client"
	"github.com/viant/mcp/protocol/server"
	"github.com/viant/mcp/schema"
	"mime"
	"path/filepath"
)

type (
	Implementer struct {
		*implementer.Base
		config *Config
		fs     afs.Service
	}

	Config struct {
		BaseURL string
		Options []storage.Option
	}
)

func (i *Implementer) ListResources(ctx context.Context, request *schema.ListResourcesRequest) (*schema.ListResourcesResult, *jsonrpc.Error) {
	objects, err := i.fs.List(ctx, i.config.BaseURL, i.config.Options...)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	var resources []schema.Resource
	for _, object := range objects {
		if object.IsDir() {
			continue
		}
		name := object.Name()
		ext := filepath.Ext(name)
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		resource := &schema.Resource{
			Name:     name,
			MimeType: &mimeType,
			Uri:      object.URL(),
		}
		resources = append(resources, *resource)
	}
	return &schema.ListResourcesResult{Resources: resources}, nil
}

func (i *Implementer) ReadResource(ctx context.Context, request *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
	object, err := i.fs.Object(ctx, request.Params.Uri)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	data, err := i.fs.DownloadWithURL(ctx, request.Params.Uri, i.config.Options...)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}

	name := object.Name()
	ext := filepath.Ext(name)
	mimeType := mime.TypeByExtension(ext)

	var text string
	var blob string
	if isBinary(data) {
		blob = base64.StdEncoding.EncodeToString(data)
	} else {
		text = string(data)
	}

	result := schema.ReadResourceResult{}
	content := schema.ReadResourceResultContentsElem{
		MimeType: &mimeType,
		Uri:      object.URL(),
		Blob:     blob,
		Text:     text,
	}
	result.Contents = append(result.Contents, content)
	return &result, nil
}

func (i *Implementer) Implements(method string) bool {
	switch method {
	case schema.MethodResourcesList, schema.MethodResourcesRead, schema.MethodSubscribe, schema.MethodUnsubscribe:
		return true
	}
	return false
}

func isBinary(data []byte) bool {
	const maxBytes = 8000
	n := min(maxBytes, len(data))
	// Heuristic: if more than 30% of the bytes are non-printable (excluding newline, tab), treat it as binary
	nonPrintable := 0
	for i := 0; i < n; i++ {
		b := data[i]
		if (b < 32 || b > 126) && b != '\n' && b != '\r' && b != '\t' {
			nonPrintable++
		}
	}
	ratio := float64(nonPrintable) / float64(n)
	return ratio > 0.3
}

func New(config *Config) server.NewImplementer {
	return func(_ context.Context, notifier transport.Notifier, logger logger.Logger, client client.Operations) server.Implementer {
		base := implementer.New(notifier, logger, client)
		return &Implementer{
			fs:     afs.New(),
			config: config,
			Base:   base,
		}
	}
}
