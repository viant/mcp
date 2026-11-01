package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	streamauth "github.com/viant/jsonrpc/transport/server/auth"
	"github.com/viant/mcp-protocol/authorization"
	protocol "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server/auth"
)

func TestParallelHTTPServersKeepBFFStoresIsolated(t *testing.T) {
	ctx := context.Background()
	stores := []streamauth.Store{streamauth.NewMemoryStore(time.Minute, time.Hour, time.Minute), streamauth.NewMemoryStore(time.Minute, time.Hour, time.Minute)}
	servers := make([]*httptest.Server, 2)
	for i := range servers {
		probe := func(w http.ResponseWriter, r *http.Request) {
			broker, err := auth.New(&auth.Config{Policy: &authorization.Policy{Global: &authorization.Authorization{}}})
			if err != nil {
				http.Error(w, "broker failed", 500)
				return
			}
			broker.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(w, r)
		}
		s, err := New(WithNewHandler(protocol.WithDefaultHandler(ctx, func(*protocol.DefaultHandler) error { return nil })), WithBFFAuthStore(stores[i]), WithCustomHTTPHandler("/probe", probe))
		if err != nil {
			t.Fatal(err)
		}
		servers[i] = httptest.NewServer(s.HTTP(ctx, "127.0.0.1:0").Handler)
		t.Cleanup(servers[i].Close)
	}
	var workers sync.WaitGroup
	for i := range servers {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			for n := 0; n < 10; n++ {
				request, _ := http.NewRequestWithContext(ctx, http.MethodPost, servers[index].URL+"/probe", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"probe","arguments":{}}}`))
				request.Header.Set("Authorization", "Bearer synthetic-credential")
				response, err := http.DefaultClient.Do(request)
				if err != nil {
					t.Error(err)
					return
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				cookies := response.Cookies()
				if response.StatusCode != http.StatusNoContent || len(cookies) != 1 {
					t.Errorf("status=%d cookies=%d", response.StatusCode, len(cookies))
					return
				}
				if _, err := stores[index].Get(ctx, cookies[0].Value); err != nil {
					t.Errorf("own listener store did not receive grant: %v", err)
					return
				}
				if _, err := stores[1-index].Get(ctx, cookies[0].Value); err == nil {
					t.Error("grant crossed listeners")
					return
				}
			}
		}(i)
	}
	workers.Wait()
}
