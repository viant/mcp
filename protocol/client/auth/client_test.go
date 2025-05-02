package auth

import (
	"context"
	"encoding/json"
	"fmt"
	client2 "github.com/viant/mcp/protocol/client/auth/client"
	"github.com/viant/mcp/protocol/client/auth/flow"
	"github.com/viant/mcp/protocol/client/auth/meta"
	"github.com/viant/mcp/protocol/client/auth/mock"
	"github.com/viant/mcp/protocol/client/auth/store"
	transport2 "github.com/viant/mcp/protocol/client/auth/transport"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// MockAuthFlow is a mock implementation of the AuthFlow interface
type MockAuthFlow struct {
	TokenFunc func(ctx context.Context, config *oauth2.Config, options ...flow.Option) (*oauth2.Token, error)
}

func (m *MockAuthFlow) Token(ctx context.Context, config *oauth2.Config, options ...flow.Option) (*oauth2.Token, error) {
	return m.TokenFunc(ctx, config, options...)
}

func TestClientWithMockServer(t *testing.T) {
	// Create mock auth server
	mockServer, err := mock.NewHTTPTestAuthorizationServer()
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer mockServer.Close()

	// Create a resource server that requires authentication
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s", scope="resource", authorization_server="%s", resource_metadata="%s/resource-metadata"`, mockServer.Issuer, mockServer.Issuer, mockServer.Issuer))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// If we have an auth header, consider it valid and return success
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer resourceServer.Close()

	// Create a mock auth flow that returns a predefined token
	mockAuthFlow := &MockAuthFlow{
		TokenFunc: func(ctx context.Context, config *oauth2.Config, options ...flow.Option) (*oauth2.Token, error) {
			// Simulate successful token acquisition
			return &oauth2.Token{
				AccessToken:  "mock_access_token",
				TokenType:    "Bearer",
				RefreshToken: "mock_refresh_token",
				Expiry:       time.Now().Add(time.Hour),
			}, nil
		},
	}

	// Create client config
	clientConfig := client2.NewConfig(
		mockServer.ClientID,
		mockServer.ClientSecret,
		oauth2.Endpoint{
			AuthURL:   mockServer.Issuer + "/authorize",
			TokenURL:  mockServer.Issuer + "/token",
			AuthStyle: oauth2.AuthStyleInHeader,
		},
		"openid", "profile",
	)

	// Create memory store with the client config
	memStore := store.NewMemoryStore(store.WithClient(clientConfig))

	// Add authorization server metadata to the store
	authServerMetadata := &meta.AuthorizationServerMetadata{
		Issuer:                mockServer.Issuer,
		AuthorizationEndpoint: mockServer.Issuer + "/authorize",
		TokenEndpoint:         mockServer.Issuer + "/token",
	}
	err = memStore.AddAuthorizationServerMetadata(authServerMetadata)
	if err != nil {
		t.Fatalf("Failed to add auth server metadata: %v", err)
	}

	// Create transport with mock auth flow
	rt, err := transport2.New(
		transport2.WithStore(memStore),
		transport2.WithAuthFlow(mockAuthFlow),
	)
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	// Create HTTP client with the transport
	client := &http.Client{Transport: rt}

	// Test accessing a protected resource
	resp, err := client.Get(resourceServer.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify response
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify that the token was stored
	token, found := memStore.LookupToken(store.TokenKey{Issuer: mockServer.Issuer, Scopes: ""})
	if !found {
		t.Error("Token not found in store")
	}
	if token == nil {
		t.Error("Token is nil")
	} else if token.AccessToken != "mock_access_token" {
		t.Errorf("Expected access token 'mock_access_token', got '%s'", token.AccessToken)
	}
}

// TestClientWithFullMockServer tests the OAuth2 client with a complete mock authorization flow
func TestClientWithFullMockServer(t *testing.T) {
	// Create mock auth server
	mockServer, err := mock.NewHTTPTestAuthorizationServer()
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer mockServer.Close()

	// Create a resource server that requires authentication
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// Return 401 with WWW-Authenticate header pointing to our mock auth server
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s", scope="resource", authorization_server="%s", resource_metadata="%s/resource-metadata"`, mockServer.Issuer, mockServer.Issuer, mockServer.Issuer))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// If we have an auth header, consider it valid and return success
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer resourceServer.Close()

	// Create a mock auth flow that simulates the authorization code flow
	mockAuthFlow := &MockAuthFlow{
		TokenFunc: func(ctx context.Context, config *oauth2.Config, options ...flow.Option) (*oauth2.Token, error) {
			// In a real flow, this would open a browser and get user consent
			// For testing, we'll simulate the exchange directly

			// Create a request to the token endpoint
			tokenReq, err := http.NewRequest("POST", config.Endpoint.TokenURL, strings.NewReader(
				"grant_type=authorization_code&code=test_authorization_code&redirect_uri=http://localhost:8080/callback",
			))
			if err != nil {
				return nil, err
			}

			// Set basic auth with client credentials
			tokenReq.SetBasicAuth(config.ClientID, config.ClientSecret)
			tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			// Make the request
			httpClient := &http.Client{}
			resp, err := httpClient.Do(tokenReq)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()

			// Parse the response
			var tokenResp struct {
				AccessToken  string `json:"access_token"`
				TokenType    string `json:"token_type"`
				RefreshToken string `json:"refresh_token"`
				ExpiresIn    int    `json:"expires_in"`
				IDToken      string `json:"id_token"`
			}

			if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
				return nil, err
			}

			// Create and return the token
			return &oauth2.Token{
				AccessToken:  tokenResp.AccessToken,
				TokenType:    tokenResp.TokenType,
				RefreshToken: tokenResp.RefreshToken,
				Expiry:       time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
			}, nil
		},
	}

	// Create client config
	clientConfig := client2.NewConfig(
		mockServer.ClientID,
		mockServer.ClientSecret,
		oauth2.Endpoint{
			AuthURL:   mockServer.Issuer + "/authorize",
			TokenURL:  mockServer.Issuer + "/token",
			AuthStyle: oauth2.AuthStyleInHeader,
		},
		"openid", "profile",
	)

	// Create memory store with the client config
	memStore := store.NewMemoryStore(store.WithClient(clientConfig))

	// Add authorization server metadata to the store
	authServerMetadata := &meta.AuthorizationServerMetadata{
		Issuer:                mockServer.Issuer,
		AuthorizationEndpoint: mockServer.Issuer + "/authorize",
		TokenEndpoint:         mockServer.Issuer + "/token",
	}
	err = memStore.AddAuthorizationServerMetadata(authServerMetadata)
	if err != nil {
		t.Fatalf("Failed to add auth server metadata: %v", err)
	}

	// Create transport with mock auth flow
	rt, err := transport2.New(
		transport2.WithStore(memStore),
		transport2.WithAuthFlow(mockAuthFlow),
	)
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	// Create HTTP client with the transport
	client := &http.Client{Transport: rt}

	// Test accessing a protected resource
	resp, err := client.Get(resourceServer.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify response
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify that a token was stored
	token, found := memStore.LookupToken(store.TokenKey{Issuer: mockServer.Issuer, Scopes: ""})
	if !found {
		t.Error("Token not found in store")
	}
	if token == nil {
		t.Error("Token is nil")
	}
}

// TestClientWithIDToken tests the OAuth2 client with ID token support
func TestClientWithIDToken(t *testing.T) {
	// Create mock auth server
	mockServer, err := mock.NewHTTPTestAuthorizationServer()
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}
	defer mockServer.Close()

	// Create a resource server that requires authentication
	resourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="%s", scope="openid", authorization_server="%s", resource_metadata="%s/resource-metadata"`, mockServer.Issuer, mockServer.Issuer, mockServer.Issuer))
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer resourceServer.Close()

	// Create a mock auth flow that returns a token with ID token
	mockAuthFlow := &MockAuthFlow{
		TokenFunc: func(ctx context.Context, config *oauth2.Config, options ...flow.Option) (*oauth2.Token, error) {
			// Create a token with an ID token
			token := &oauth2.Token{
				AccessToken:  "mock_access_token",
				TokenType:    "Bearer",
				RefreshToken: "mock_refresh_token",
				Expiry:       time.Now().Add(time.Hour),
			}

			// Add ID token to the extra fields
			token = token.WithExtra(map[string]interface{}{
				"id_token": "mock_id_token",
			})

			return token, nil
		},
	}

	// Create client config with openid scope
	clientConfig := client2.NewConfig(
		mockServer.ClientID,
		mockServer.ClientSecret,
		oauth2.Endpoint{
			AuthURL:   mockServer.Issuer + "/authorize",
			TokenURL:  mockServer.Issuer + "/token",
			AuthStyle: oauth2.AuthStyleInHeader,
		},
		"openid", "profile",
	)

	// Create memory store with the client config
	memStore := store.NewMemoryStore(store.WithClient(clientConfig))

	// Add authorization server metadata to the store
	authServerMetadata := &meta.AuthorizationServerMetadata{
		Issuer:                mockServer.Issuer,
		AuthorizationEndpoint: mockServer.Issuer + "/authorize",
		TokenEndpoint:         mockServer.Issuer + "/token",
	}
	err = memStore.AddAuthorizationServerMetadata(authServerMetadata)
	if err != nil {
		t.Fatalf("Failed to add auth server metadata: %v", err)
	}

	// Create transport with mock auth flow
	rt, err := transport2.New(
		transport2.WithStore(memStore),
		transport2.WithAuthFlow(mockAuthFlow),
	)
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}

	// Create HTTP client with the transport
	client := &http.Client{Transport: rt}

	// Test accessing a protected resource
	resp, err := client.Get(resourceServer.URL)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify response
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify that the token was stored
	token, found := memStore.LookupToken(store.TokenKey{Issuer: mockServer.Issuer, Scopes: ""})
	if !found {
		t.Error("Token not found in store")
	}
	if token == nil {
		t.Error("Token is nil")
	} else {
		// Check if the token has an ID token
		extra := token.Extra("id_token")
		if extra == nil {
			t.Error("ID token not found in token extra fields")
		} else if idToken, ok := extra.(string); !ok || idToken != "mock_id_token" {
			t.Errorf("Expected ID token 'mock_id_token', got '%v'", extra)
		}
	}
}
