package transport

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/viant/mcp/client/auth/store"
	"golang.org/x/oauth2"
)

type opaqueScopeStore struct{ store.Store }

func TestCredentialScopeTracksCookiesAndStoredTokenWithoutExposingValues(t *testing.T) {
	target, _ := url.Parse("https://fixture.test/mcp")
	jar, _ := cookiejar.New(nil)
	rt, err := New(WithCookieJar(jar))
	if err != nil {
		t.Fatal(err)
	}
	first, ok := rt.CredentialScope(target)
	if !ok {
		t.Fatal("default store has no credential witness")
	}
	jar.SetCookies(target, []*http.Cookie{{Name: "session", Value: "synthetic-cookie", Path: "/"}})
	second, ok := rt.CredentialScope(target)
	if !ok || first == second {
		t.Fatal("cookie rotation not witnessed")
	}
	if err := rt.Store().AddToken(store.TokenKey{Issuer: "fixture", Scopes: "metadata"}, &oauth2.Token{AccessToken: "synthetic-token", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	third, ok := rt.CredentialScope(target)
	if !ok || third == second {
		t.Fatal("stored credential rotation not witnessed")
	}
	again, ok := rt.CredentialScope(target)
	if !ok || again != third {
		t.Fatal("unchanged credential scope unstable")
	}
	for _, scope := range []string{first, second, third} {
		if len(scope) != 64 || strings.Contains(scope, "synthetic") {
			t.Fatal("credential witness is not a digest")
		}
	}
	opaque, err := New(WithStore(opaqueScopeStore{store.NewMemoryStore()}))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opaque.CredentialScope(target); ok {
		t.Fatal("unsupported custom store was trusted")
	}
}

type orderedScopeJar struct{ cookies []*http.Cookie }

func (j *orderedScopeJar) Cookies(*url.URL) []*http.Cookie               { return j.cookies }
func (j *orderedScopeJar) SetCookies(_ *url.URL, cookies []*http.Cookie) { j.cookies = cookies }

func TestCredentialScopePreservesOrderOfDuplicateCookieNames(t *testing.T) {
	target, _ := url.Parse("https://fixture.test/mcp")
	first, second := &http.Cookie{Name: "session", Value: "first"}, &http.Cookie{Name: "session", Value: "second"}
	jar := &orderedScopeJar{cookies: []*http.Cookie{first, second}}
	rt, err := New(WithCookieJar(jar))
	if err != nil {
		t.Fatal(err)
	}
	before, ok := rt.CredentialScope(target)
	if !ok {
		t.Fatal("missing witness")
	}
	jar.cookies = []*http.Cookie{second, first}
	after, ok := rt.CredentialScope(target)
	if !ok || before == after {
		t.Fatal("different effective cookie wire order collided")
	}
}

func TestCredentialScopeDetectsStoreReplacementAtSameRevision(t *testing.T) {
	target, _ := url.Parse("https://fixture.test/mcp")
	first, second := store.NewMemoryStore(), store.NewMemoryStore()
	key := store.TokenKey{Issuer: "fixture", Scopes: "metadata"}
	if err := first.AddToken(key, &oauth2.Token{AccessToken: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := second.AddToken(key, &oauth2.Token{AccessToken: "second"}); err != nil {
		t.Fatal(err)
	}
	rt, err := New(WithStore(first))
	if err != nil {
		t.Fatal(err)
	}
	before, ok := rt.CredentialScope(target)
	if !ok {
		t.Fatal("missing witness")
	}
	WithStore(second)(rt)
	after, ok := rt.CredentialScope(target)
	if !ok || before == after {
		t.Fatal("different store credentials at equal revision collided")
	}
}
