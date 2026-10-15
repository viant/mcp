package providerregistry

import (
	"context"
	"fmt"
	"testing"
	"time"

	authcfg "github.com/viant/mcp/client/auth/config"
	"github.com/viant/mcp/client/auth/link/provider"
)

type testLoader struct {
	docs      map[string]*oauthprovider.Document
	listErr   error
	dirExists *bool
	dirErr    error
}

func (l *testLoader) List(ctx context.Context) ([]string, error) {
	if l.listErr != nil {
		return nil, l.listErr
	}
	names := make([]string, 0, len(l.docs))
	for name := range l.docs {
		names = append(names, name)
	}
	return names, nil
}

func (l *testLoader) Load(ctx context.Context, name string) (*oauthprovider.Document, error) {
	doc, ok := l.docs[name]
	if !ok {
		return nil, fmt.Errorf("not found: %s", name)
	}
	clone := *doc
	return &clone, nil
}

// checkedLoader additionally reports directory existence.
type checkedLoader struct{ testLoader }

func (l *checkedLoader) DirExists(ctx context.Context) (bool, error) {
	if l.dirErr != nil {
		return false, l.dirErr
	}
	if l.dirExists == nil {
		return true, nil
	}
	return *l.dirExists, nil
}

func fileProvider(id, issuer string) *oauthprovider.Document {
	return &oauthprovider.Document{
		OAuthProvider: authcfg.OAuthProvider{
			ID:            id,
			Issuer:        issuer,
			DefaultClient: "web",
			Clients: map[string]*authcfg.OAuthClient{
				"web": {ConfigURL: "scy://" + id, RefreshLead: "15m"},
			},
		},
	}
}

func inlineProvider(id, issuer, refreshLead string) *authcfg.OAuthProvider {
	return &authcfg.OAuthProvider{
		ID:            id,
		Issuer:        issuer,
		DefaultClient: "inline",
		Clients: map[string]*authcfg.OAuthClient{
			"inline": {ConfigURL: "scy://" + id, RefreshLead: refreshLead},
		},
	}
}

// TestLoadFailsClosedOnListError: only a genuinely missing oauth/providers
// directory is an empty registry; IO errors fail closed.
func TestLoadFailsClosedOnListError(t *testing.T) {
	// Loader without DirExists: an error must fail closed, never read as
	// "no providers configured".
	plain := NewWithLoader(&testLoader{listErr: fmt.Errorf("permission denied")})
	if _, err := plain.Provider(context.Background(), "any"); err == nil || IsNotFound(err) {
		t.Fatalf("list IO error must fail closed, got %v", err)
	}

	// DirExists=false: missing directory means empty registry (typed not-found).
	missing := false
	absent := NewWithLoader(&checkedLoader{testLoader: testLoader{listErr: fmt.Errorf("no such file"), dirExists: &missing}})
	if _, err := absent.Provider(context.Background(), "any"); !IsNotFound(err) {
		t.Fatalf("missing directory must read as empty registry, got %v", err)
	}

	// DirExists=true with a List error: fail closed.
	present := true
	broken := NewWithLoader(&checkedLoader{testLoader: testLoader{listErr: fmt.Errorf("disk error"), dirExists: &present}})
	if _, err := broken.Provider(context.Background(), "any"); err == nil || IsNotFound(err) {
		t.Fatalf("IO error with existing directory must fail closed, got %v", err)
	}

	// DirExists itself erroring: fail closed.
	unknown := NewWithLoader(&checkedLoader{testLoader: testLoader{listErr: fmt.Errorf("io"), dirErr: fmt.Errorf("stat failed")}})
	if _, err := unknown.Provider(context.Background(), "any"); err == nil || IsNotFound(err) {
		t.Fatalf("unverifiable directory must fail closed, got %v", err)
	}
}

// TestLoadFailsClosedOnDecodeError: a provider file that fails to load or
// validate poisons the whole load rather than serving a partial registry.
func TestLoadFailsClosedOnDecodeError(t *testing.T) {
	bad := fileProvider("bad", "not-a-url")
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{
		"good": fileProvider("good", "https://idp.example.com"),
		"bad":  bad,
	}})
	if _, err := registry.Provider(context.Background(), "good"); err == nil {
		t.Fatalf("an invalid provider document must fail the whole load")
	}
}

func TestRegisterInlineOverlay(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{}})
	inline := inlineProvider("inline-dev6", "https://idp-inline.example.com", "20m")
	if err := registry.RegisterInline(context.Background(), inline); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Registration is idempotent for an identical definition.
	if err := registry.RegisterInline(context.Background(), inline.Clone()); err != nil {
		t.Fatalf("identical re-registration must succeed: %v", err)
	}
	// A different definition under the same id conflicts.
	changed := inline.Clone()
	changed.Issuer = "https://other.example.com"
	if err := registry.RegisterInline(context.Background(), changed); err == nil {
		t.Fatalf("conflicting inline definitions must fail")
	}

	resolved, err := registry.ResolveProvider(context.Background(), "inline-dev6")
	if err != nil || resolved.Issuer != "https://idp-inline.example.com" {
		t.Fatalf("overlay provider must resolve: %v %+v", err, resolved)
	}
	matched, err := registry.MatchIssuer(context.Background(), "https://idp-inline.example.com/")
	if err != nil || matched.ID != "inline-dev6" {
		t.Fatalf("overlay provider must match by normalized issuer: %v", err)
	}
	if lead := registry.MaxRefreshLead(context.Background()); lead != 20*time.Minute {
		t.Fatalf("overlay refresh leads must extend the scan horizon, got %s", lead)
	}

	// Invalid inline providers are rejected before registration.
	if err := registry.RegisterInline(context.Background(), &authcfg.OAuthProvider{ID: "no-issuer"}); err == nil {
		t.Fatalf("invalid inline provider must fail validation")
	}
	if err := registry.RegisterInline(context.Background(), inlineProvider("", "https://x.example.com", "")); err == nil {
		t.Fatalf("inline provider without a stable id must fail")
	}
}

// TestRegisterInlineFileProviderWins: a workspace registry file provider with
// the same id wins when issuers agree, and conflicts fail closed.
func TestRegisterInlineFileProviderWins(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{
		"corp": fileProvider("corp", "https://idp.example.com"),
	}})
	aligned := inlineProvider("corp", "https://idp.example.com/", "20m")
	if err := registry.RegisterInline(context.Background(), aligned); err != nil {
		t.Fatalf("issuer-aligned inline definition must be accepted: %v", err)
	}
	// Resolution serves the file definition (its default client name differs).
	resolved, err := registry.ResolveProvider(context.Background(), "corp")
	if err != nil || resolved.DefaultClient != "web" {
		t.Fatalf("file provider must win resolution: %v %+v", err, resolved)
	}
	conflicting := inlineProvider("corp", "https://rogue.example.com", "20m")
	if err := registry.RegisterInline(context.Background(), conflicting); err == nil {
		t.Fatalf("issuer conflict with a file provider must fail closed")
	}
}

// TestMatchIssuerOverlayAmbiguity: two inline providers sharing an issuer
// hard-fail issuer matching, same as file providers.
func TestMatchIssuerOverlayAmbiguity(t *testing.T) {
	registry := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{}})
	if err := registry.RegisterInline(context.Background(), inlineProvider("a", "https://shared.example.com", "")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.RegisterInline(context.Background(), inlineProvider("b", "https://shared.example.com/", "")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := registry.MatchIssuer(context.Background(), "https://shared.example.com"); err == nil {
		t.Fatalf("ambiguous inline issuers must hard-fail MatchIssuer")
	}
	// A file provider with the issuer wins over any overlay entries.
	fileFirst := NewWithLoader(&testLoader{docs: map[string]*oauthprovider.Document{
		"file": fileProvider("file", "https://shared.example.com"),
	}})
	if err := fileFirst.RegisterInline(context.Background(), inlineProvider("c", "https://another.example.com", "")); err != nil {
		t.Fatalf("register: %v", err)
	}
	matched, err := fileFirst.MatchIssuer(context.Background(), "https://shared.example.com")
	if err != nil || matched.ID != "file" {
		t.Fatalf("file provider must win issuer matching: %v", err)
	}
}
