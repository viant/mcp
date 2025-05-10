package custom

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/viant/afs"
	"github.com/viant/afs/storage"
	"github.com/viant/jsonrpc"
	"github.com/viant/jsonrpc/transport"
	protoclient "github.com/viant/mcp-protocol/client"
	"github.com/viant/mcp-protocol/logger"
	"github.com/viant/mcp-protocol/schema"
	protoserver "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp-protocol/syncmap"
	"mime"
	"path/filepath"
	"sync"
	"time"
)

type (
	Implementer struct {
		*protoserver.DefaultImplementer
		config    *Config
		fs        afs.Service
		snapshot  *syncmap.Map[string, storage.Object]
		mutex     sync.RWMutex
		stopWatch context.CancelFunc // to shut the watcher down
	}

	Config struct {
		BaseURL string
		Options []storage.Option
	}
)

func (i *Implementer) watchLoop(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second) // make interval configurable
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := i.pollChanges(ctx); err != nil {
				i.Logger.Error(ctx, fmt.Sprintf("failed to poll changes: %v", err))
			}
		}
	}
}

// Subscribe adds the URI to the subscription map.
func (i *Implementer) Subscribe(ctx context.Context, request *schema.SubscribeRequest) (*schema.SubscribeResult, *jsonrpc.Error) {
	i.Subscription.Put(request.Params.Uri, true)
	object, err := i.fs.Object(ctx, request.Params.Uri)
	if err != nil {
		return nil, jsonrpc.NewInternalError(err.Error(), nil)
	}
	if !object.IsDir() {
		i.snapshot.Put(object.URL(), object)
	}
	if object.IsDir() {
		objects, err := i.fs.List(ctx, request.Params.Uri)
		if err != nil {
			return nil, jsonrpc.NewInternalError(err.Error(), nil)
		}
		for _, asset := range objects {
			if asset.IsDir() {
				continue
			}
			i.snapshot.Put(asset.URL(), asset)
		}
	}
	return &schema.SubscribeResult{}, nil
}

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

// Implements returns true if the method is supported by this implementer
func (i *Implementer) Implements(method string) bool {
	switch method {
	case schema.MethodResourcesList, schema.MethodResourcesRead, schema.MethodSubscribe, schema.MethodUnsubscribe:
		return true
	}
	return i.DefaultImplementer.Implements(method)
}

func (i *Implementer) pollChanges(ctx context.Context) error {
	for _, object := range i.snapshot.Values() {
		if object.IsDir() {
			continue
		}
		current, err := i.fs.Object(ctx, object.URL())
		if err != nil {
			return err
		}
		if current.ModTime().After(object.ModTime()) {
			if err = i.notifyResourceUpdated(ctx, object.URL()); err != nil {
				return err
			}
			i.snapshot.Put(current.URL(), current)
		}
	}
	return nil
}

func (i *Implementer) notifyResourceUpdated(ctx context.Context, uri string) error {
	notification, err := jsonrpc.NewNotification(schema.MethodNotificationResourceUpdated, map[string]string{"uri": uri})
	if err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}
	return i.Notifier.Notify(ctx, notification)
}

func (i *Implementer) ensureWatcher() {
	i.mutex.Lock()
	defer i.mutex.Unlock()
	if i.stopWatch != nil { // already running
		return
	}
	wctx, cancel := context.WithCancel(context.Background())
	i.stopWatch = cancel
	go i.watchLoop(wctx)
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

// New creates a new Implementer instance
func New(config *Config) protoserver.NewImplementer {
	return func(_ context.Context, notifier transport.Notifier, logger logger.Logger, client protoclient.Operations) (protoserver.Implementer, error) {
		base := protoserver.NewDefaultImplementer(notifier, logger, client)
		ret := &Implementer{
			fs:                 afs.New(),
			config:             config,
			DefaultImplementer: base,
			snapshot:           syncmap.NewMap[string, storage.Object](),
		}
		ret.ensureWatcher()
		return ret, nil
	}
}
