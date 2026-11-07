package manager

import (
	"context"
	"testing"
	"time"

	"github.com/viant/mcp"
	authcfg "github.com/viant/mcp/client/auth/config"
	"os"
	"path/filepath"
)

func testBindingProvider() *authcfg.OAuthProvider {
	return &authcfg.OAuthProvider{
		ID:            "approved-provider",
		Issuer:        "https://idp.test/",
		DefaultClient: "web",
		Clients: map[string]*authcfg.OAuthClient{
			"web": {ConfigURL: "mem://client.json", RedirectURI: "https://app.test/cb", Confidential: true, UsePKCE: true},
		},
	}
}

func newBindingManager(t *testing.T) *Manager {
	t.Helper()
	registry, err := authcfg.NewStaticRegistry(testBindingProvider())
	if err != nil {
		t.Fatalf("NewStaticRegistry() error = %v", err)
	}
	mgr, err := New(nil,
		WithProviderRegistry(registry),
		WithAuthBindingStore(NewAuthBindingStore(&bindingTestState{root: t.TempDir()})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return mgr
}

func TestAuthBindingStore_SaveLoadInvalidate(t *testing.T) {
	store := NewAuthBindingStore(&bindingTestState{root: t.TempDir()})
	ctx := context.Background()
	binding := &MCPAuthBinding{
		ServerName:  "dev6",
		Origin:      "https://mcp.test",
		MetadataURL: "https://mcp.test/.well-known/oauth-protected-resource",
		ProviderRef: "approved-provider",
		ClientRef:   "web",
		Issuer:      "https://idp.test",
		Resource:    "https://mcp.test/mcp",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := store.Save(ctx, binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// A fresh store over the same state directory reloads the persisted file.
	reloaded := NewAuthBindingStore(store.state)
	loaded, err := reloaded.Load(ctx, "dev6")
	if err != nil || loaded == nil || loaded.ProviderRef != "approved-provider" {
		t.Fatalf("Load() = %+v, %v", loaded, err)
	}
	store.Invalidate(ctx, "dev6")
	if loaded, _ := NewAuthBindingStore(store.state).Load(ctx, "dev6"); loaded != nil {
		t.Fatalf("binding survived Invalidate(): %+v", loaded)
	}
}

func TestAuthBindingStore_ExpiredBindingIgnored(t *testing.T) {
	store := NewAuthBindingStore(&bindingTestState{root: t.TempDir()})
	ctx := context.Background()
	if err := store.Save(ctx, &MCPAuthBinding{
		ServerName:  "dev6",
		ProviderRef: "approved-provider",
		Issuer:      "https://idp.test",
		ExpiresAt:   time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if loaded, _ := store.Load(ctx, "dev6"); loaded != nil {
		t.Fatalf("expired binding served: %+v", loaded)
	}
}

func TestLearnAuthBinding_Validation(t *testing.T) {
	mgr := newBindingManager(t)
	ctx := context.Background()

	// Unknown issuers never receive known client credentials.
	mgr.learnAuthBinding(ctx, "dev6", "https://mcp.test/mcp",
		"https://rogue-idp.test/", "https://mcp.test/mcp", "https://mcp.test/.well-known/oauth-protected-resource", nil)
	if binding, _ := mgr.bindings.Load(ctx, "dev6"); binding != nil {
		t.Fatalf("binding learned for an unapproved issuer: %+v", binding)
	}

	// Non-HTTPS metadata is rejected.
	mgr.learnAuthBinding(ctx, "dev6", "https://mcp.test/mcp",
		"https://idp.test/", "https://mcp.test/mcp", "http://mcp.test/.well-known/oauth-protected-resource", nil)
	if binding, _ := mgr.bindings.Load(ctx, "dev6"); binding != nil {
		t.Fatalf("binding learned from non-https metadata: %+v", binding)
	}

	// A resource on another origin than the MCP transport is rejected.
	mgr.learnAuthBinding(ctx, "dev6", "https://mcp.test/mcp",
		"https://idp.test/", "https://other.test/mcp", "https://mcp.test/.well-known/oauth-protected-resource", nil)
	if binding, _ := mgr.bindings.Load(ctx, "dev6"); binding != nil {
		t.Fatalf("binding learned for a cross-origin resource: %+v", binding)
	}

	// A valid challenge learns the approved provider and its default client.
	mgr.learnAuthBinding(ctx, "dev6", "https://mcp.test/mcp",
		"https://idp.test/", "https://mcp.test/mcp", "https://mcp.test/.well-known/oauth-protected-resource", []string{"plan:read"})
	binding, err := mgr.bindings.Load(ctx, "dev6")
	if err != nil || binding == nil {
		t.Fatalf("valid challenge did not learn a binding: %v", err)
	}
	if binding.ProviderRef != "approved-provider" || binding.ClientRef != "web" {
		t.Fatalf("learned binding = %+v, want approved-provider/web", binding)
	}
}

func TestApplyLearnedBinding_ExplicitConfigWins(t *testing.T) {
	mgr := newBindingManager(t)
	ctx := context.Background()
	if err := mgr.bindings.Save(ctx, &MCPAuthBinding{
		ServerName:  "dev6",
		Origin:      "https://mcp.test",
		ProviderRef: "approved-provider",
		ClientRef:   "web",
		Issuer:      "https://idp.test",
		Resource:    "https://mcp.test/mcp",
		ExpiresAt:   time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Explicit providerRef stays untouched.
	explicit := &mcp.ClientAuth{Mode: authcfg.ModeOAuth, ProviderRef: "explicit-provider"}
	mgr.applyLearnedBinding(ctx, "dev6", "https://mcp.test/mcp", explicit)
	if explicit.ProviderRef != "explicit-provider" {
		t.Fatalf("learned binding overwrote explicit providerRef: %q", explicit.ProviderRef)
	}

	// Challenge-mode config adopts the learned provider eagerly.
	challenge := &mcp.ClientAuth{Mode: authcfg.ModeOAuth}
	mgr.applyLearnedBinding(ctx, "dev6", "https://mcp.test/mcp", challenge)
	if challenge.ProviderRef != "approved-provider" || challenge.ClientRef != "web" || challenge.Resource != "https://mcp.test/mcp" {
		t.Fatalf("learned binding not applied: %+v", challenge)
	}

	// A binding for another origin is invalidated instead of applied.
	moved := &mcp.ClientAuth{Mode: authcfg.ModeOAuth}
	mgr.applyLearnedBinding(ctx, "dev6", "https://relocated.test/mcp", moved)
	if moved.ProviderRef != "" {
		t.Fatalf("binding applied across origins: %+v", moved)
	}
	if binding, _ := mgr.bindings.Load(ctx, "dev6"); binding != nil {
		t.Fatalf("origin-mismatched binding survived: %+v", binding)
	}
}

func TestEvictUserServer(t *testing.T) {
	mgr, err := New(nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	mgr.pool = map[string]map[string]*entry{
		"user-1:conv-1": {"dev6": &entry{usedAt: time.Now()}, "other": &entry{usedAt: time.Now()}},
		"user-2:conv-2": {"dev6": &entry{usedAt: time.Now()}},
	}
	mgr.EvictUserServer("user-1", "dev6")
	if _, ok := mgr.pool["user-1:conv-1"]["dev6"]; ok {
		t.Fatalf("user-1 dev6 entry survived eviction")
	}
	if _, ok := mgr.pool["user-1:conv-1"]["other"]; !ok {
		t.Fatalf("eviction removed an unrelated server entry")
	}
	if _, ok := mgr.pool["user-2:conv-2"]["dev6"]; !ok {
		t.Fatalf("eviction crossed user isolation")
	}
}

type bindingTestState struct{ root string }

func (s *bindingTestState) StatePath(_ context.Context, logical string) (string, error) {
	path := filepath.Join(s.root, logical)
	return path, os.MkdirAll(path, 0700)
}
