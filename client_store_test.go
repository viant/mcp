package mcp

import (
	"context"
	"crypto"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"github.com/viant/mcp/client/auth/store"
	"github.com/viant/scy"
	"github.com/viant/scy/cred"
	_ "github.com/viant/scy/kms/blowfish"
	"golang.org/x/oauth2"
)

const testEncryptionKey = "blowfish://default"

func writeOAuth2Config(t *testing.T) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "oauth.json")
	oauthConfig := &cred.Oauth2Config{}
	oauthConfig.ClientID = "steward-web"
	oauthConfig.ClientSecret = "test-secret"
	oauthConfig.Endpoint.AuthURL = "https://idp.example.com/authorize"
	oauthConfig.Endpoint.TokenURL = "https://idp.example.com/token"
	resource := scy.NewResource(reflect.TypeOf(cred.Oauth2Config{}), configPath, testEncryptionKey)
	if err := scy.New().Store(context.Background(), scy.NewSecret(oauthConfig, resource)); err != nil {
		t.Fatal(err)
	}
	return configPath
}

// TestGetOAuthHTTPClient_InjectedStoreRetainsClientConfigs covers the
// persistent auth-store fix: OAuth client configs loaded from
// OAuth2ConfigURL must be installed into an injected Store rather than being
// discarded when Auth.Store is non-nil.
func TestGetOAuthHTTPClient_InjectedStoreRetainsClientConfigs(t *testing.T) {
	injected := store.NewMemoryStore()
	options := &ClientOptions{
		Name: "dev6",
		Auth: &ClientAuth{
			OAuth2ConfigURL: []string{writeOAuth2Config(t)},
			EncryptionKey:   testEncryptionKey,
			Store:           injected,
		},
	}
	httpClient, err := options.getOAuthHTTPClient(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, httpClient)

	clientConfig, found := injected.LookupClientConfig("https://idp.example.com")
	if assert.True(t, found, "loaded oauth client config must be installed into the injected store") {
		assert.Equal(t, "steward-web", clientConfig.ClientID)
	}
	assert.Same(t, injected, options.AuthStore(), "the injected store must back the auth transport")
}

// TestGetOAuthHTTPClient_DefaultMemoryStoreStillLoadsConfigs guards legacy
// behaviour: without an injected store the loaded configs continue to seed
// the default in-memory store.
func TestGetOAuthHTTPClient_DefaultMemoryStoreStillLoadsConfigs(t *testing.T) {
	options := &ClientOptions{
		Name: "dev6",
		Auth: &ClientAuth{
			OAuth2ConfigURL: []string{writeOAuth2Config(t)},
			EncryptionKey:   testEncryptionKey,
		},
	}
	httpClient, err := options.getOAuthHTTPClient(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, httpClient)

	authStore := options.AuthStore()
	if assert.NotNil(t, authStore) {
		clientConfig, found := authStore.LookupClientConfig("https://idp.example.com")
		if assert.True(t, found) {
			assert.Equal(t, "steward-web", clientConfig.ClientID)
		}
	}
}

func TestInstallClientConfig(t *testing.T) {
	assert.NoError(t, store.InstallClientConfig(nil, nil), "nil-safe")
}

// rawStore is a deliberately non-normalizing Store: keys are used verbatim.
// It models arbitrary injected host implementations that must not be handed
// unnormalized issuer keys.
type rawStore struct {
	clients map[string]*oauth2.Config
}

func (r *rawStore) LookupClientConfig(issuer string) (*oauth2.Config, bool) {
	client, ok := r.clients[issuer]
	return client, ok
}

func (r *rawStore) AddClientConfig(issuer string, client *oauth2.Config) error {
	if r.clients == nil {
		r.clients = map[string]*oauth2.Config{}
	}
	r.clients[issuer] = client
	return nil
}

func (r *rawStore) AddAuthorizationServerMetadata(*meta.AuthorizationServerMetadata) error {
	return nil
}
func (r *rawStore) LookupAuthorizationServerMetadata(string) (*meta.AuthorizationServerMetadata, bool) {
	return nil, false
}
func (r *rawStore) AddIssuerPublicKeys(string, map[string]crypto.PublicKey) error { return nil }
func (r *rawStore) LookupIssuerPublicKeys(string) (map[string]crypto.PublicKey, bool) {
	return nil, false
}
func (r *rawStore) AddToken(store.TokenKey, *oauth2.Token) error     { return nil }
func (r *rawStore) LookupToken(store.TokenKey) (*oauth2.Token, bool) { return nil, false }

// TestInstallClientConfig_NormalizedIssuerForCustomStore asserts that the
// issuer key handed to an arbitrary injected Store is already normalized —
// custom implementations cannot be assumed to normalize keys themselves.
func TestInstallClientConfig_NormalizedIssuerForCustomStore(t *testing.T) {
	custom := &rawStore{}
	client := &oauth2.Config{
		ClientID: "steward-web",
		Endpoint: oauth2.Endpoint{
			// leading whitespace and derivation quirks must not leak into the key
			AuthURL:  " https://idp.example.com/authorize",
			TokenURL: "https://idp.example.com/token",
		},
	}
	assert.NoError(t, store.InstallClientConfig(custom, client))

	found, ok := custom.LookupClientConfig("https://idp.example.com")
	if assert.True(t, ok, "custom store must receive the canonical normalized issuer key, got keys %v", keysOf(custom.clients)) {
		assert.Equal(t, "steward-web", found.ClientID)
	}
	assert.Equal(t, "https://idp.example.com", store.ConfigIssuer(client), "ConfigIssuer must return the normalized issuer")
}

func keysOf(m map[string]*oauth2.Config) []string {
	var keys []string
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
