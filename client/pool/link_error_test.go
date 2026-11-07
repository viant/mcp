// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"fmt"
	"testing"

	authcfg "github.com/viant/mcp/client/auth/config"
)

func TestLinkRequiredExposesTypedOutcomeToEmbeddingHosts(t *testing.T) {
	upstream := &authcfg.OAuthLinkRequiredError{ServerName: "analytics", ProviderRef: "identity", Resource: "https://source.example/mcp"}
	required, ok := LinkRequired(fmt.Errorf("host unavailable: %w", upstream))
	if !ok || required.ServerName != "analytics" || required.ProviderRef != "identity" || required.Resource != "https://source.example/mcp" {
		t.Fatalf("typed link-required outcome=%+v ok=%v", required, ok)
	}
	if _, ok := LinkRequired(fmt.Errorf("provider temporarily unavailable")); ok {
		t.Fatal("transient provider failure became link-required")
	}
}
