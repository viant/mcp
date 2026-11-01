package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	streamauth "github.com/viant/jsonrpc/transport/server/auth"
)

func TestBFFDefaultsConcurrentServiceCreation(t *testing.T) {
	previousStore, previousName := defaultBFFGrantStore, defaultBFFAuthCookieName
	t.Cleanup(func() { SetDefaultBFFAuthStore(previousStore); SetDefaultBFFAuthCookieName(previousName) })
	stores := []streamauth.Store{streamauth.NewMemoryStore(time.Minute, time.Hour, time.Minute), streamauth.NewMemoryStore(time.Minute, time.Hour, time.Minute)}
	SetDefaultBFFAuthStore(stores[0])
	var workers sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			for i := 0; i < 100; i++ {
				if index%2 == 0 {
					SetDefaultBFFAuthStore(stores[i%len(stores)])
					SetDefaultBFFAuthCookieName([]string{"session-a", "session-b"}[i%2])
				} else {
					service, err := New(&Config{})
					if err != nil || service.bffGrantStore == nil || service.bffAuthCookieName == "" {
						t.Errorf("invalid default snapshot: %v", err)
					}
				}
			}
		}(worker)
	}
	close(start)
	workers.Wait()
}

func TestBFFCookieConcurrentLazyStore(t *testing.T) {
	previousStore, previousName := defaultBFFGrantStore, defaultBFFAuthCookieName
	t.Cleanup(func() { SetDefaultBFFAuthStore(previousStore); SetDefaultBFFAuthCookieName(previousName) })
	SetDefaultBFFAuthStore(nil)
	SetDefaultBFFAuthCookieName("synthetic-session")
	service, err := New(&Config{})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			response := httptest.NewRecorder()
			service.ensureBFFAuthCookie(response, httptest.NewRequest(http.MethodPost, "/mcp", nil))
			cookies := response.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != "synthetic-session" {
				t.Error("missing BFF cookie")
			}
		}()
	}
	close(start)
	workers.Wait()
}

func TestBFFServiceKeepsConfiguredStoreAndCookieSnapshot(t *testing.T) {
	previousStore, previousName := currentBFFDefaults()
	t.Cleanup(func() { SetDefaultBFFAuthStore(previousStore); SetDefaultBFFAuthCookieName(previousName) })
	first := streamauth.NewMemoryStore(time.Minute, time.Hour, time.Minute)
	second := streamauth.NewMemoryStore(time.Minute, time.Hour, time.Minute)
	SetDefaultBFFAuthStore(first)
	SetDefaultBFFAuthCookieName("initial-session")
	service, err := New(&Config{BFFGrantStore: second})
	if err != nil {
		t.Fatal(err)
	}
	SetDefaultBFFAuthStore(nil)
	SetDefaultBFFAuthCookieName("later-session")
	if service.bffStore(context.Background()) != second || service.bffCookieName(context.Background()) != "initial-session" {
		t.Fatal("later defaults changed an existing service's configured store or cookie")
	}
}
