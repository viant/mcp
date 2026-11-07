// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	cfg "github.com/viant/mcp/client/config"
)

// catalogTransportCredentialScope witnesses only transport hooks actually used
// by this source. It does not authorize execution or expose cookie/token values.
func (m *Manager) catalogTransportCredentialScope(ctx context.Context, name string, options *cfg.MCPClient) (string, bool, error) {
	if m.IsLocalClient(name) || options == nil || options.ClientOptions == nil || options.Transport.URL == "" {
		return "", true, nil
	}
	endpoint, err := url.Parse(options.Transport.URL)
	if err != nil {
		return "", false, err
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return "", true, nil
	}
	cookies, err := m.catalogCookies(ctx, endpoint, options)
	if err != nil {
		return "", false, err
	}
	authScope := ""
	useTransportAuth := false
	if options.Auth != nil && !options.IsDelegatedAuth() {
		useTransportAuth = options.Auth.BackendForFrontend == nil || *options.Auth.BackendForFrontend || len(options.Auth.OAuth2ConfigURL) > 0
	}
	if useTransportAuth {
		rt := m.authRT
		if m.authRTProvider != nil {
			if selected := m.authRTProvider(ctx); selected != nil {
				rt = selected
			}
		}
		if rt != nil {
			witness, ok := rt.CredentialScopeForMetadata(endpoint, options.MetadataCacheIgnoredCookies)
			if !ok {
				return "", false, nil
			}
			authScope = fmt.Sprintf("%p:%s", rt, witness)
		}
	}
	raw, _ := json.Marshal([]any{strings.TrimSpace(options.Transport.URL), cookies, authScope})
	return fmt.Sprintf("%x", sha256.Sum256(raw)), true, nil
}

// catalogCookies selects exactly the cookies the configured HTTP client sends.
func (m *Manager) catalogCookies(ctx context.Context, endpoint *url.URL, options *cfg.MCPClient) ([][2]string, error) {
	var jar http.CookieJar
	if m.jarProvider != nil {
		var err error
		jar, err = m.jarProvider(ctx)
		if err != nil {
			return nil, err
		}
	} else if m.cookieJar != nil {
		jar = m.cookieJar
	} else {
		jar = options.CookieJar
	}
	// Preserve actual wire order: duplicate cookie names can be order-sensitive.
	cookies := [][2]string{}
	if jar != nil {
		for _, cookie := range jar.Cookies(endpoint) {
			if slices.Contains(options.MetadataCacheIgnoredCookies, cookie.Name) {
				continue
			}
			cookies = append(cookies, [2]string{cookie.Name, cookie.Value})
		}
	}
	return cookies, nil
}
