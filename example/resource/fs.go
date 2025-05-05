package resource

import (
	"context"
	"encoding/base64"
	"github.com/viant/afs"
	"github.com/viant/afs/storage"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
	proto "github.com/viant/mcp-protocol/server"
	"mime"
	"path/filepath"
)

type Config struct {
	BaseURL string
	Options []storage.Option
}

type FileSystem struct {
	config *Config
	fs     afs.Service
}

func (r *FileSystem) Resources(ctx context.Context) ([]*proto.ResourceEntry, error) {
	objects, err := r.fs.List(ctx, r.config.BaseURL, r.config.Options...)
	if err != nil {
		return nil, err
	}
	var resources []*proto.ResourceEntry
	for _, obj := range objects {
		if obj.IsDir() {
			continue
		}
		name := obj.Name()
		ext := filepath.Ext(name)
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		resources = append(resources, &proto.ResourceEntry{
			Metadata: schema.Resource{
				MimeType: &mimeType,
				Name:     obj.Name(),
				Uri:      obj.URL(),
			},
			Handler: func(ctx context.Context, request *schema.ReadResourceRequest) (*schema.ReadResourceResult, *jsonrpc.Error) {
				return r.Read(ctx, request.Params.Uri)
			},
		})
	}
	return resources, nil
}

func (r *FileSystem) Read(ctx context.Context, URI string) (*schema.ReadResourceResult, *jsonrpc.Error) {
	obj, err := r.fs.Object(ctx, URI)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	data, err := r.fs.DownloadWithURL(ctx, URI, r.config.Options...)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	ext := filepath.Ext(obj.Name())
	mimeType := mime.TypeByExtension(ext)
	var text, blob string
	if isBinary(data) {
		blob = base64.StdEncoding.EncodeToString(data)
	} else {
		text = string(data)
	}
	result := &schema.ReadResourceResult{}
	result.Contents = append(result.Contents, schema.ReadResourceResultContentsElem{
		MimeType: &mimeType,
		Uri:      obj.URL(),
		Blob:     blob,
		Text:     text,
	})
	return result, nil

}

// isBinary returns true if data has non-printable ratio > 30%.
func isBinary(data []byte) bool {
	const maxBytes = 8000
	n := maxBytes
	if len(data) < n {
		n = len(data)
	}
	non := 0
	for i := 0; i < n; i++ {
		b := data[i]
		if (b < 32 || b > 126) && b != '\n' && b != '\r' && b != '\t' {
			non++
		}
	}
	return float64(non)/float64(n) > 0.3
}

// NewFileSystem creates a new file system resource
func NewFileSystem(config *Config) *FileSystem {
	return &FileSystem{
		config: config,
		fs:     afs.New(),
	}
}
