package transport

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
)

// CredentialScope summarizes current credentials without exposing their values.
// Unknown custom stores cannot attest changes and must bypass metadata caching.
func (r *RoundTripper) CredentialScope(resource *url.URL) (string, bool) {
	if r == nil || resource == nil {
		return "", false
	}
	witness, ok := r.store.(interface{ CredentialScope() (string, bool) })
	if !ok {
		return "", false
	}
	// Preserve actual wire order: duplicate cookie names can be order-sensitive.
	storeScope, valid := witness.CredentialScope()
	if !valid {
		return "", false
	}
	cookies := [][2]string{}
	if r.jar != nil {
		for _, cookie := range r.jar.Cookies(resource) {
			cookies = append(cookies, [2]string{cookie.Name, cookie.Value})
		}
	}
	raw, _ := json.Marshal([]any{storeScope, cookies, r.useBFF, r.bffHeader, r.disableTokenFallback, r.Global})
	return fmt.Sprintf("%x", sha256.Sum256(raw)), true
}
