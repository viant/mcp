//go:build transport

package client_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/viant/jsonrpc"
	sseclient "github.com/viant/jsonrpc/transport/client/http/sse"
	streamingclient "github.com/viant/jsonrpc/transport/client/http/streamable"
	"github.com/viant/mcp-protocol/schema"
	serverproto "github.com/viant/mcp-protocol/server"
	clientpkg "github.com/viant/mcp/client"
	"github.com/viant/mcp/server"
	"github.com/viant/mcp/server/namespace"
	"github.com/viant/mcp/server/oob"
)

type oobFormData struct {
	Prompt string
}

type oobActionResponse struct {
	Action string                 `json:"action"`
	Data   map[string]interface{} `json:"data,omitempty"`
}

func TestOOBFormActions_SSE(t *testing.T) {
	baseURL, store, stop := startOOBFormServer(t, false)
	defer stop()

	ctx := context.Background()
	transport, err := sseclient.New(ctx, baseURL+"/sse")
	require.NoError(t, err)

	cli := clientpkg.New("TestClient", "1.0", transport)
	_, err = cli.Initialize(ctx)
	require.NoError(t, err)

	assertOOBFormActions(t, ctx, cli, baseURL, store)
}

func TestOOBFormActions_Streaming(t *testing.T) {
	baseURL, store, stop := startOOBFormServer(t, true)
	defer stop()

	ctx := context.Background()
	transport, err := streamingclient.New(ctx, baseURL+"/mcp")
	require.NoError(t, err)

	cli := clientpkg.New("TestClient", "1.0", transport)
	_, err = cli.Initialize(ctx)
	require.NoError(t, err)

	assertOOBFormActions(t, ctx, cli, baseURL, store)
}

func assertOOBFormActions(t *testing.T, ctx context.Context, cli *clientpkg.Client, baseURL string, store *oob.MemoryStore[oobFormData]) {
	t.Helper()

	id := createOOBFormPending(t, ctx, cli)
	assertPending(t, store, id, true)
	resp := postFormAction(t, baseURL+"/oob/submit", id, map[string]string{
		"email": "user@example.com",
		"code":  "1234",
	})
	require.Equal(t, "accept", resp.Action)
	require.Equal(t, "user@example.com", resp.Data["email"])
	require.Equal(t, "1234", resp.Data["code"])
	assertPending(t, store, id, false)

	id = createOOBFormPending(t, ctx, cli)
	assertPending(t, store, id, true)
	resp = postFormAction(t, baseURL+"/oob/cancel", id, nil)
	require.Equal(t, "cancel", resp.Action)
	assertPending(t, store, id, false)

	id = createOOBFormPending(t, ctx, cli)
	assertPending(t, store, id, true)
	resp = postFormAction(t, baseURL+"/oob/reject", id, nil)
	require.Equal(t, "reject", resp.Action)
	assertPending(t, store, id, false)
}

func createOOBFormPending(t *testing.T, ctx context.Context, cli *clientpkg.Client) string {
	t.Helper()
	params, err := schema.NewCallToolRequestParams[struct{}]("needs-oob-form", struct{}{})
	require.NoError(t, err)
	res, err := cli.CallTool(ctx, params)
	require.NoError(t, err)
	require.NotNil(t, res.StructuredContent)
	rawURL, ok := res.StructuredContent["oobUrl"].(string)
	require.True(t, ok)
	id := oobIDFromURL(t, rawURL)
	require.NotEmpty(t, id)
	return id
}

func oobIDFromURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Query().Get("id")
}

func assertPending(t *testing.T, store *oob.MemoryStore[oobFormData], id string, expect bool) {
	t.Helper()
	_, ok, err := store.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, expect, ok)
}

func postFormAction(t *testing.T, endpoint, id string, fields map[string]string) oobActionResponse {
	t.Helper()
	form := url.Values{}
	form.Set("id", id)
	for k, v := range fields {
		form.Set(k, v)
	}
	resp, err := http.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var out oobActionResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func startOOBFormServer(t *testing.T, useStreaming bool) (string, *oob.MemoryStore[oobFormData], func()) {
	t.Helper()
	store := oob.NewMemoryStore[oobFormData]()
	provider := namespace.NewProvider(nil)
	var baseURL string
	mgr := &oob.Manager[oobFormData]{
		Provider: provider,
		Store:    store,
		CallbackBuilder: func(id string) string {
			return strings.TrimRight(baseURL, "/") + "/oob/form?id=" + id
		},
	}

	newHandler := serverproto.WithDefaultHandler(context.Background(), func(h *serverproto.DefaultHandler) error {
		return serverproto.RegisterTool[*struct{}, *struct{}](h.Registry, "needs-oob-form", "Trigger OOB form", func(ctx context.Context, _ *struct{}) (*schema.CallToolResult, *jsonrpc.Error) {
			_, url, err := mgr.Create(ctx, oob.Spec[oobFormData]{Kind: "form", Alias: "demo", Resource: "example", Data: oobFormData{Prompt: "Provide credentials"}})
			if err != nil {
				return nil, jsonrpc.NewInternalError(err.Error(), nil)
			}
			return &schema.CallToolResult{
				StructuredContent: map[string]interface{}{"oobUrl": url},
				Content: []schema.CallToolResultContentElem{
					schema.TextContent{Text: url, Type: "text"},
				},
			}, nil
		})
	})

	mux := http.NewServeMux()
	oobBase := oob.NamespaceFromPending(store, func(r *http.Request) (string, error) {
		return r.URL.Query().Get("id"), nil
	}, func(ctx context.Context, p oob.Pending[oobFormData], w http.ResponseWriter, _ *http.Request) error {
		desc, _ := namespace.FromContext(ctx)
		_, _ = fmt.Fprintf(w, "namespace=%s; prompt=%s", desc.Name, p.Data.Prompt)
		return nil
	})
	mux.Handle("/oob/form", oobBase)
	mux.HandleFunc("/oob/submit", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.FormValue("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if _, err := mgr.Complete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		out := oobActionResponse{Action: "accept", Data: map[string]interface{}{
			"email": r.FormValue("email"),
			"code":  r.FormValue("code"),
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/oob/cancel", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.FormValue("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if _, err := mgr.Cancel(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		out := oobActionResponse{Action: "cancel"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/oob/reject", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.FormValue("id")
		if id == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if _, err := mgr.Complete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		out := oobActionResponse{Action: "reject"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	srv, err := server.New(
		server.WithNewHandler(newHandler),
		server.WithCustomHTTPHandler("/oob/form", oobBase.ServeHTTP),
		server.WithCustomHTTPHandler("/oob/submit", mux.ServeHTTP),
		server.WithCustomHTTPHandler("/oob/cancel", mux.ServeHTTP),
		server.WithCustomHTTPHandler("/oob/reject", mux.ServeHTTP),
		server.WithImplementation(schema.Implementation{Name: "oob-form-test", Version: "0.1"}),
	)
	require.NoError(t, err)
	if useStreaming {
		srv.UseStreamableHTTP(true)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	baseURL = "http://" + ln.Addr().String()
	httpSrv := srv.HTTP(context.Background(), ln.Addr().String())
	go func() { _ = httpSrv.Serve(ln) }()
	return baseURL, store, func() { _ = httpSrv.Close() }
}
