package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// FileJar is a thin wrapper around the standard cookiejar.Jar that persists
// cookies to a JSON file on each update and reloads them on startup.
// It is good enough for CLI and single-host services.
type FileJar struct {
	mu    sync.RWMutex
	inner *cookiejar.Jar
	path  string
}

type persistedCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires"`
	Secure   bool      `json:"secure"`
	HttpOnly bool      `json:"httpOnly"`
}

type cookieSnapshot struct {
	Cookies []persistedCookie `json:"cookies"`
}

// NewFileJar creates a cookie jar persisted at path.
func NewFileJar(path string) (*FileJar, error) {
	inner, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	j := &FileJar{inner: inner, path: path}
	_ = j.load()
	return j, nil
}

func (j *FileJar) Cookies(u *neturl.URL) []*http.Cookie {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.inner.Cookies(u)
}

func (j *FileJar) SetCookies(u *neturl.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inner.SetCookies(u, cookies)
	_ = j.save(u)
}

func (j *FileJar) save(u *neturl.URL) error {
	snap := cookieSnapshot{}
	// cookiejar.Jar does not expose enumeration; we rely on the fact that we
	// persist on every SetCookies and rehydrate on load. So we only persist the
	// last written cookies if called. For better coverage, callers can call save
	// after establishing sessions.
	// We approximate enumeration by saving cookies for a wide synthetic URL set
	// if needed in future.
	// Here we rely on SetCookies path to have been called with all relevant cookies.
	// To improve, we could keep our own index inside SetCookies; implement that now.
	// Extract from inner by remembering last write set is lossy; keep index ourselves.
	// For simplicity: store the cookies passed in last SetCookies is insufficient.
	// We'll keep a growing index across calls by loading existing, merging, and writing.
	existing := map[string]persistedCookie{}
	if data, err := os.ReadFile(j.path); err == nil {
		var old cookieSnapshot
		if json.Unmarshal(data, &old) == nil {
			for _, pc := range old.Cookies {
				key := pc.Domain + "|" + pc.Path + "|" + pc.Name
				existing[key] = pc
			}
		}
	}
	// We cannot enumerate current jar; but SetCookies just wrote to it, so we update/merge
	// the provided cookies into existing and drop expired.
	for _, c := range j.inner.Cookies(&neturl.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}) {
		// Normalize domain/path for host-only cookies and missing path
		domain := strings.TrimSpace(c.Domain)
		if domain == "" {
			// Use request host without port as cookie domain for host-only cookies
			host := u.Host
			if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
				host = h
			}
			// Remove leading dots just in case
			domain = strings.TrimPrefix(host, ".")
		}
		path := c.Path
		if strings.TrimSpace(path) == "" {
			path = "/"
		}
		key := domain + "|" + path + "|" + c.Name
		pc := persistedCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   domain,
			Path:     path,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		}
		if !pc.Expires.IsZero() && time.Now().After(pc.Expires) {
			delete(existing, key)
			continue
		}
		existing[key] = pc
	}
	for _, v := range existing {
		snap.Cookies = append(snap.Cookies, v)
	}
	if err := os.MkdirAll(filepath.Dir(j.path), 0o700); err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}

func (j *FileJar) load() error {
	data, err := os.ReadFile(j.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var snap cookieSnapshot
	if err = json.Unmarshal(data, &snap); err != nil {
		return err
	}
	// Rehydrate into the inner jar using synthetic URLs constructed from cookie domain/path
	for _, pc := range snap.Cookies {
		if !pc.Expires.IsZero() && time.Now().After(pc.Expires) {
			continue
		}
		scheme := "https"
		if !pc.Secure {
			scheme = "http"
		}
		host := strings.TrimPrefix(pc.Domain, ".")
		if host == "" {
			// Backward-compat: host-only cookies were saved without domain.
			// Rehydrate them for common dev hosts to avoid prompting again.
			for _, h := range []string{"localhost", "127.0.0.1"} {
				u := &neturl.URL{Scheme: scheme, Host: h, Path: pc.Path}
				j.inner.SetCookies(u, []*http.Cookie{{
					Name:     pc.Name,
					Value:    pc.Value,
					Domain:   h,
					Path:     pc.Path,
					Expires:  pc.Expires,
					Secure:   pc.Secure,
					HttpOnly: pc.HttpOnly,
				}})
				fmt.Printf("[mcp/filejar] rehydrated hostless cookie name=%s to host=%s path=%s secure=%t from=%s\n", pc.Name, h, pc.Path, pc.Secure, j.path)
			}
			continue
		}
		u := &neturl.URL{Scheme: scheme, Host: host, Path: pc.Path}
		j.inner.SetCookies(u, []*http.Cookie{{
			Name:     pc.Name,
			Value:    pc.Value,
			Domain:   pc.Domain,
			Path:     pc.Path,
			Expires:  pc.Expires,
			Secure:   pc.Secure,
			HttpOnly: pc.HttpOnly,
		}})
		// Persist normalized snapshot so future runs don't see hostless cookies
		_ = j.save(u)
	}
	return nil
}
