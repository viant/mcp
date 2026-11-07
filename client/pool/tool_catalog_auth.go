// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"sync"

	authcfg "github.com/viant/mcp/client/auth/config"
)

type catalogAuthKey struct{}
type catalogAuthUse struct {
	mu       sync.Mutex
	token    string
	observed bool
}

// This request-local witness records the credential actually selected by the
// existing delegated transport, including a refresh on a 401. It is used only
// to prevent publishing catalog metadata under a stale credential fingerprint.
type catalogCredentialResolver struct{ authcfg.CredentialResolver }

func (r catalogCredentialResolver) Resolve(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	credential, err := r.CredentialResolver.Resolve(ctx, requirement)
	recordCatalogCredential(ctx, credential, err)
	return credential, err
}
func (r catalogCredentialResolver) Refresh(ctx context.Context, requirement authcfg.Requirement) (*authcfg.Credential, error) {
	credential, err := r.CredentialResolver.Refresh(ctx, requirement)
	recordCatalogCredential(ctx, credential, err)
	return credential, err
}
func recordCatalogCredential(ctx context.Context, credential *authcfg.Credential, err error) {
	if witness, ok := ctx.Value(catalogAuthKey{}).(*catalogAuthUse); ok {
		witness.mu.Lock()
		defer witness.mu.Unlock()
		witness.observed = true
		witness.token = ""
		if credential != nil && err == nil {
			witness.token = credential.Token
		}
	}
}
