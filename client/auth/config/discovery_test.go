package config

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/viant/mcp-protocol/oauth2/meta"
)

func TestDiscoverProtectedResource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resource":                 "https://mcp6.example.com/mcp",
			"authorization_servers":    []string{"https://idp-dev6.example.com/"},
			"scopes_supported":         []string{"plan:create", "plan:edit", "plan:read"},
			"bearer_methods_supported": []string{"header"},
		})
	}))
	defer server.Close()

	metadata, err := DiscoverProtectedResource(context.Background(), server.URL, &DiscoveryOptions{AllowHTTP: true, ExpectedOrigin: server.URL})
	assert.NoError(t, err)
	assert.Equal(t, "https://mcp6.example.com/mcp", metadata.Resource)
	assert.Equal(t, []string{"https://idp-dev6.example.com/"}, metadata.AuthorizationServers)
}

func TestDiscoverProtectedResource_RequiresHTTPS(t *testing.T) {
	_, err := DiscoverProtectedResource(context.Background(), "http://mcp.example.com/.well-known/oauth-protected-resource",
		&DiscoveryOptions{ExpectedOrigin: "http://mcp.example.com"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "https")
}

// TestDiscoverProtectedResource_FailsClosedWithoutExpectedOrigin asserts the
// SSRF guard is opt-out, not opt-in: an empty ExpectedOrigin is rejected
// unless the caller explicitly allows cross-origin fetches.
func TestDiscoverProtectedResource_FailsClosedWithoutExpectedOrigin(t *testing.T) {
	_, err := DiscoverProtectedResource(context.Background(), "https://mcp6.example.com/.well-known/oauth-protected-resource", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ExpectedOrigin")

	_, err = DiscoverProtectedResource(context.Background(), "https://mcp6.example.com/.well-known/oauth-protected-resource",
		&DiscoveryOptions{AllowHTTP: true})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ExpectedOrigin")
}

func TestDiscoverProtectedResource_RejectsMissingAuthorizationServers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"resource": "https://mcp6.example.com/mcp"})
	}))
	defer server.Close()
	_, err := DiscoverProtectedResource(context.Background(), server.URL, &DiscoveryOptions{AllowHTTP: true, ExpectedOrigin: server.URL})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authorization_servers")
}

func TestDiscoverProtectedResource_BoundedRedirects(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+r.URL.Path+"x", http.StatusFound)
	}))
	defer server.Close()
	_, err := DiscoverProtectedResource(context.Background(), server.URL, &DiscoveryOptions{AllowHTTP: true, MaxRedirects: 2, ExpectedOrigin: server.URL})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redirect")
}

func TestDiscoverProtectedResource_BoundedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resource":"` + strings.Repeat("x", 4096) + `"}`))
	}))
	defer server.Close()
	_, err := DiscoverProtectedResource(context.Background(), server.URL, &DiscoveryOptions{AllowHTTP: true, MaxBodyBytes: 128, ExpectedOrigin: server.URL})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestDiscoverProtectedResource_ExpectedOriginAcceptsSameOrigin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resource":              "https://mcp6.example.com/mcp",
			"authorization_servers": []string{"https://idp-dev6.example.com/"},
		})
	}))
	defer server.Close()

	metadata, err := DiscoverProtectedResource(context.Background(), server.URL+"/.well-known/oauth-protected-resource",
		&DiscoveryOptions{AllowHTTP: true, ExpectedOrigin: server.URL})
	assert.NoError(t, err)
	assert.NotNil(t, metadata)
}

// TestDiscoverProtectedResource_RejectsCrossOriginInitialURL covers the
// challenge-driven SSRF vector: a hostile WWW-Authenticate challenge pointing
// at a foreign origin must be rejected before any request is made.
func TestDiscoverProtectedResource_RejectsCrossOriginInitialURL(t *testing.T) {
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("cross-origin metadata URL must never be fetched")
	}))
	defer attacker.Close()

	_, err := DiscoverProtectedResource(context.Background(), attacker.URL+"/.well-known/oauth-protected-resource",
		&DiscoveryOptions{AllowHTTP: true, ExpectedOrigin: "https://mcp6.example.com"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "origin")
}

// TestDiscoverProtectedResource_RejectsCrossOriginRedirect asserts every
// redirect is validated against the expected origin, not just the initial URL.
func TestDiscoverProtectedResource_RejectsCrossOriginRedirect(t *testing.T) {
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("cross-origin redirect target must never be fetched")
	}))
	defer attacker.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/metadata", http.StatusFound)
	}))
	defer server.Close()

	_, err := DiscoverProtectedResource(context.Background(), server.URL+"/.well-known/oauth-protected-resource",
		&DiscoveryOptions{AllowHTTP: true, ExpectedOrigin: server.URL})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "origin")
}

func TestDiscoverProtectedResource_RejectsInvalidExpectedOrigin(t *testing.T) {
	_, err := DiscoverProtectedResource(context.Background(), "https://mcp6.example.com/.well-known/oauth-protected-resource",
		&DiscoveryOptions{ExpectedOrigin: "not-an-origin"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected origin")
}

func TestDiscoverProtectedResource_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer server.Close()
	_, err := DiscoverProtectedResource(context.Background(), server.URL, &DiscoveryOptions{AllowHTTP: true, ExpectedOrigin: server.URL})
	assert.Error(t, err)
}

// TestDiscoverProtectedResource_AllowCrossOriginOptOut asserts the explicit
// opt-out permits a fetch without an ExpectedOrigin pin.
func TestDiscoverProtectedResource_AllowCrossOriginOptOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"resource":              "https://mcp6.example.com/mcp",
			"authorization_servers": []string{"https://idp-dev6.example.com/"},
		})
	}))
	defer server.Close()

	metadata, err := DiscoverProtectedResource(context.Background(), server.URL,
		&DiscoveryOptions{AllowHTTP: true, AllowCrossOrigin: true})
	assert.NoError(t, err)
	assert.NotNil(t, metadata)
}

func TestChallengeMetadataURL(t *testing.T) {
	const want = "https://mcp6.example.com/.well-known/oauth-protected-resource"
	// unquoted and quoted parameter values
	assert.Equal(t, want, ChallengeMetadataURL(`Bearer resource_metadata=`+want))
	assert.Equal(t, want, ChallengeMetadataURL(`Bearer realm="mcp", resource_metadata="`+want+`"`))
	// scheme and parameter names match case-insensitively
	assert.Equal(t, want, ChallengeMetadataURL(`bearer resource_metadata="`+want+`"`))
	assert.Equal(t, want, ChallengeMetadataURL(`BEARER Resource_Metadata="`+want+`"`))
	assert.Equal(t, want, ChallengeMetadataURL(`Bearer RESOURCE_METADATA=`+want))
	// commas inside quoted parameter values do not split parameters
	assert.Equal(t, want, ChallengeMetadataURL(`Bearer realm="a,b", resource_metadata="`+want+`"`))
	// parameter list without a scheme token still parses
	assert.Equal(t, want, ChallengeMetadataURL(`resource_metadata="`+want+`"`))
	// non-Bearer schemes never yield a metadata URL
	assert.Equal(t, "", ChallengeMetadataURL(`Basic resource_metadata="`+want+`"`))
	assert.Equal(t, "", ChallengeMetadataURL(`Bearer realm="mcp"`))
	assert.Equal(t, "", ChallengeMetadataURL("Bearer"))
	assert.Equal(t, "", ChallengeMetadataURL(""))
}

func TestRequirement_ApplyProtectedResourceMetadata(t *testing.T) {
	metadata := &meta.ProtectedResourceMetadata{
		Resource:             "https://mcp6.example.com/mcp",
		AuthorizationServers: []string{"https://idp-dev6.example.com/"},
	}

	// fills empty fields
	requirement := &Requirement{ServerName: "dev6"}
	assert.NoError(t, requirement.ApplyProtectedResourceMetadata(metadata))
	assert.Equal(t, "https://mcp6.example.com/mcp", requirement.Resource)
	assert.Equal(t, "https://idp-dev6.example.com", requirement.Issuer)

	// exact match passes
	requirement = &Requirement{ServerName: "dev6", Resource: "https://mcp6.example.com/mcp", Issuer: "https://idp-dev6.example.com"}
	assert.NoError(t, requirement.ApplyProtectedResourceMetadata(metadata))

	// resource mismatch fails — /mcp2 does not satisfy /mcp
	requirement = &Requirement{ServerName: "dev6", Resource: "https://mcp6.example.com/mcp2"}
	assert.Error(t, requirement.ApplyProtectedResourceMetadata(metadata))

	// issuer not among authorization servers fails
	requirement = &Requirement{ServerName: "dev6", Issuer: "https://other-idp.example.com"}
	assert.Error(t, requirement.ApplyProtectedResourceMetadata(metadata))

	// nil metadata fails
	requirement = &Requirement{ServerName: "dev6"}
	assert.Error(t, requirement.ApplyProtectedResourceMetadata(nil))

	// ambiguous authorization servers leave issuer unset
	requirement = &Requirement{ServerName: "dev6"}
	assert.NoError(t, requirement.ApplyProtectedResourceMetadata(&meta.ProtectedResourceMetadata{
		Resource:             "https://mcp6.example.com/mcp",
		AuthorizationServers: []string{"https://a.example.com", "https://b.example.com"},
	}))
	assert.Equal(t, "", requirement.Issuer)
}
