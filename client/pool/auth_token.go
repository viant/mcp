// SPDX-License-Identifier: Apache-2.0
// Forge resource client pooling extracted from Viant Agently Core.
package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	authctx "github.com/viant/mcp/client/auth/link"
	authtransport "github.com/viant/mcp/client/auth/transport"
	mcpcfg "github.com/viant/mcp/client/config"
)

// UseIDToken reports whether the MCP server config prefers using an ID token
// when authenticating outbound calls to this server.
func (m *Manager) UseIDToken(ctx context.Context, serverName string) bool {
	if m == nil {
		return false
	}
	name := strings.TrimSpace(serverName)
	if name == "" {
		return false
	}
	cfg, err := m.Options(ctx, name)
	if err != nil || cfg == nil || cfg.ClientOptions == nil || cfg.ClientOptions.Auth == nil {
		return false
	}
	return cfg.ClientOptions.Auth.UseIdToken
}

// IsDelegatedAuth reports whether the MCP server config selects delegated
// OAuth (auth.mode=oauth with providerRef/inlineProvider). Delegated servers
// resolve credentials exclusively through the installed CredentialResolver:
// workspace tokens must never be injected for them.
func (m *Manager) IsDelegatedAuth(ctx context.Context, serverName string) bool {
	if m == nil {
		return false
	}
	name := strings.TrimSpace(serverName)
	if name == "" {
		return false
	}
	cfg, err := m.Options(ctx, name)
	if err != nil || cfg == nil {
		return false
	}
	return cfg.IsDelegatedAuth()
}

// WithAuthTokenContext injects the selected auth token into context under the
// MCP auth transport key so HTTP transports can emit the appropriate Bearer header.
// This is a best-effort helper; when no token is available it returns ctx as-is.
// Local registrations preserve the original in-process authority context.
// Delegated servers are also excluded so workspace credentials never reach
// a delegated MCP transport.
func (m *Manager) WithAuthTokenContext(ctx context.Context, serverName string) context.Context {
	if ctx == nil || m == nil {
		return ctx
	}
	selectionContext := ctx
	// In-process providers consume the original verified caller context. An
	// OAuth refresh here would silently replace that captured authority.
	if m.IsLocalClient(serverName) {
		reportAuthSelection(selectionContext, AuthSelection{ServerName: serverName, Type: "local_context", Present: "not_applicable"})
		return ctx
	}
	if m.IsDelegatedAuth(ctx, serverName) {
		reportAuthSelection(selectionContext, AuthSelection{ServerName: serverName, Type: "delegated", Present: "unknown"})
		return ctx
	}
	debugMCPAuthEnsure(serverName, ctx, "before_ensure", m.UseIDToken(ctx, serverName))
	useIDToken := m.UseIDToken(ctx, serverName)
	if m.prepareTokens != nil {
		if prepared := m.prepareTokens(ctx, serverName, useIDToken); prepared != nil {
			ctx = prepared
		}
	}
	useID := useIDToken
	tok := authctx.MCPAuthToken(ctx, useID)
	selectionType := "access_token"
	if useID {
		selectionType = "id_token"
	}
	if strings.TrimSpace(tok) == "" {
		reportAuthSelection(selectionContext, AuthSelection{ServerName: serverName, Type: selectionType, Present: "false"})
		return ctx
	}
	reportAuthSelection(selectionContext, AuthSelection{ServerName: serverName, Type: selectionType, Present: "true"})
	return context.WithValue(ctx, authtransport.ContextAuthTokenKey, tok)
}

func debugMCPAuthEnsure(serverName string, ctx context.Context, stage string, useID bool) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) == "" || ctx == nil {
		return
	}
	userID := strings.TrimSpace(authctx.EffectiveUserID(ctx))
	provider := strings.TrimSpace(authctx.Provider(ctx))
	tb := authctx.TokensFromContext(ctx)
	accessFP := "none"
	idFP := "none"
	selectedFP := "none"
	if tb != nil {
		accessFP = tokenFingerprint(tb.AccessToken)
		idFP = tokenFingerprint(tb.IDToken)
	}
	if strings.TrimSpace(authctx.IDToken(ctx)) != "" && idFP == "none" {
		idFP = tokenFingerprint(authctx.IDToken(ctx))
	}
	if strings.TrimSpace(authctx.Bearer(ctx)) != "" && accessFP == "none" {
		accessFP = tokenFingerprint(authctx.Bearer(ctx))
	}
	// Best effort to infer selection intent from the currently injected transport token.
	if transportToken, _ := ctx.Value(authtransport.ContextAuthTokenKey).(string); strings.TrimSpace(transportToken) != "" {
		selectedFP = tokenFingerprint(transportToken)
	}
	fmt.Fprintf(os.Stderr, "[mcp-auth] stage=%s server=%s user=%q provider=%q useID=%v access_sha=%s id_sha=%s selected_sha=%s\n",
		strings.TrimSpace(stage), strings.TrimSpace(serverName), userID, provider, useID, accessFP, idFP, selectedFP)
}

func debugMCPAuthEnsureError(serverName string, ctx context.Context, userID, provider string, useID bool, err error) {
	if strings.TrimSpace(os.Getenv("AGENTLY_DEBUG_MCP_AUTH")) == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[mcp-auth] stage=ensure_error server=%s user=%q provider=%q useID=%v err=%q\n",
		strings.TrimSpace(serverName), strings.TrimSpace(userID), strings.TrimSpace(provider), useID, strings.TrimSpace(err.Error()))
}

func tokenFingerprint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:12]
}

// usesUserTransportAuth follows the existing BFF transport selection and never
// forwards workspace credentials to delegated or explicitly anonymous peers.
func usesUserTransportAuth(options *mcpcfg.MCPClient) bool {
	if options == nil || options.ClientOptions == nil || options.Auth == nil || options.IsDelegatedAuth() {
		return false
	}
	return options.Auth.BackendForFrontend == nil || *options.Auth.BackendForFrontend || len(options.Auth.OAuth2ConfigURL) > 0
}
