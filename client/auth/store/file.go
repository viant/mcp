package store

import (
	"crypto"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/viant/mcp-protocol/oauth2/meta"
	"golang.org/x/oauth2"
)

// FileStore persists tokens to a JSON file, while delegating other
// lookups to an in-memory store. It is a lightweight way to survive
// process restarts in CLI or single-host services.
type FileStore struct {
	mu     sync.RWMutex
	path   string
	memory *memoryStore
	tokens map[TokenKey]*oauth2.Token
}

// NewFileStore creates a Store that persists tokens at the given path.
// Client configs and metadata are kept in-memory (they can be rediscovered).
func NewFileStore(path string, options ...MemoryStoreOption) Store {
	fs := &FileStore{
		path:   path,
		memory: NewMemoryStore(options...).(*memoryStore),
		tokens: map[TokenKey]*oauth2.Token{},
	}
	_ = fs.load()
	return fs
}

func (f *FileStore) LookupClientConfig(issuer string) (*oauth2.Config, bool) {
	return f.memory.LookupClientConfig(issuer)
}

func (f *FileStore) AddClientConfig(issuer string, client *oauth2.Config) error {
	return f.memory.AddClientConfig(issuer, client)
}

func (f *FileStore) AddAuthorizationServerMetadata(metadata *meta.AuthorizationServerMetadata) error {
	return f.memory.AddAuthorizationServerMetadata(metadata)
}

func (f *FileStore) LookupAuthorizationServerMetadata(issuer string) (*meta.AuthorizationServerMetadata, bool) {
	return f.memory.LookupAuthorizationServerMetadata(issuer)
}

func (f *FileStore) AddIssuerPublicKeys(issuer string, keys map[string]crypto.PublicKey) error {
	return f.memory.AddIssuerPublicKeys(issuer, keys)
}

func (f *FileStore) LookupIssuerPublicKeys(issuer string) (map[string]crypto.PublicKey, bool) {
	return f.memory.LookupIssuerPublicKeys(issuer)
}

func (f *FileStore) LookupToken(key TokenKey) (*oauth2.Token, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if t, ok := f.tokens[key]; ok {
		return t, true
	}
	return nil, false
}

func (f *FileStore) AddToken(key TokenKey, token *oauth2.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens == nil {
		f.tokens = map[TokenKey]*oauth2.Token{}
	}
	// normalize expiry zero to a past time for consistent JSON
	if token.Expiry.IsZero() {
		token.Expiry = time.Time{}
	}
	f.tokens[key] = token
	return f.save()
}

// ---- persistence ----

type fileSnapshot struct {
	Tokens map[string]*oauth2.Token `json:"tokens"`
}

func keyString(k TokenKey) string { return k.Issuer + "|" + k.Scopes }

func (f *FileStore) save() error {
	snap := fileSnapshot{Tokens: map[string]*oauth2.Token{}}
	for k, v := range f.tokens {
		snap.Tokens[keyString(k)] = v
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f *FileStore) load() error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			f.tokens = map[TokenKey]*oauth2.Token{}
			return nil
		}
		return err
	}
	var snap fileSnapshot
	if err = json.Unmarshal(data, &snap); err != nil {
		return err
	}
	f.tokens = map[TokenKey]*oauth2.Token{}
	for k, v := range snap.Tokens {
		parts := strings.SplitN(k, "|", 2)
		if len(parts) != 2 {
			continue
		}
		f.tokens[TokenKey{Issuer: parts[0], Scopes: parts[1]}] = v
	}
	return nil
}
