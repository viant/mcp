<!-- Automatically generated. Authentication and authorization guide for MCP. -->
# Authentication Guide

MCP supports securing both the server and client using OAuth2/OIDC standards.

## Authentication Modes

MCP supports two authentication modes:

1. Global Resource Protection (spec-based)
   - Protects the entire MCP server as an OAuth2-protected resource.
   - Conforms to RFC 9728 (OAuth 2.0 Protected Resource Metadata).
   - Automatically exposes `/.well-known/oauth-protected-resource` for discovery.

2. Fine-Grained Tool/Resource Control (experimental)
   - Allows per-tool or per-resource authorization checks with custom scopes or servers.
   - Uses an RPC interceptor (`auth.Authorizer`) to handle 401 challenges at the JSON-RPC layer.
   - Requires explicit wiring on both server and client sides.

## 1. Global Resource Protection (spec-based)

Follow the standard OAuth 2.1 Authorization Code Flow with PKCE. The server treats itself as a protected resource:

1. **Initial Request**: The client requests any MCP endpoint without a token. The server responds with `401 Unauthorized` and a `WWW-Authenticate` header:
   ```http
   WWW-Authenticate: Bearer realm="https://myapp.example.com", resource_metadata="https://myapp.example.com/.well-known/oauth-protected-resource"
   ```
2. **Metadata Discovery**: The client fetches `/.well-known/oauth-protected-resource` for `ProtectedResourceMetadata`.
3. **Authorization Server Discovery**: The client reads `authorization_servers` and fetches each server’s OAuth2 metadata (`/.well-known/oauth-authorization-server`).
4. **Authorization Code + PKCE**: Redirect the user to the auth endpoint with PKCE.
5. **Token Exchange**: Exchange the code at the token endpoint for an access token.
6. **Retry Request**: The client sends the original request with `Authorization: Bearer <token>`.
7. **Authorized Response**: The server validates the token and returns the requested data.

## Securing the MCP Server

Use `server.WithAuthConfig` when creating your MCP server to enable OAuth2 protection.


#### OAuth2 RoundTripper Options

When initializing the OAuth2 RoundTripper via `transport.New`, you can customize behavior with options:

- `WithStore(store.Store)`: sets the `store.Store` (e.g. `store.NewMemoryStore`) to register client configurations, cache discovery metadata, and persist/refresh tokens.
- `WithAuthFlow(flow.AuthFlow)`: selects the interactive authentication flow (default: `&flow2.BrowserFlow{}`) for PKCE or other flows (e.g., `flow.NewBrowserFlow()`, custom `flow.AuthFlow`).

These options enable your client to discover the server’s protected-resource metadata, acquire and cache tokens transparently, and replay requests with fresh `Authorization: Bearer <token>` headers.

This configuration will:
- Expose the OAuth2 metadata endpoint at `/.well-known/oauth-protected-resource`.
 - Protect all other endpoints (e.g., `/sse`, `/`) by requiring a valid Bearer token unless ExcludeURI is specified.


### Securing the MCP Client (spec-based)

On the client side, the default OAuth2 RoundTripper will:
1. Probe the MCP server and parse the `WWW-Authenticate` header.
2. Fetch protected resource metadata (`/.well-known/oauth-protected-resource`).
3. Discover the authorization server(s) and fetch their metadata (`/.well-known/oauth-authorization-server`).
4. Acquire a token via the configured auth flow (e.g., PKCE).
5. Retry the original request with `Authorization: Bearer <token>`.

Provide your OAuth2 credentials by registering a client config in the store:
```go
import (
    "context"
    "log"
    "net/http"
    "golang.org/x/oauth2"
    "github.com/viant/jsonrpc/transport/client/http/sse"
    "github.com/viant/mcp/client"
    "github.com/viant/mcp/client/auth/flow"
    "github.com/viant/mcp/client/auth/store"
    "github.com/viant/mcp/client/auth/transport"
    "github.com/viant/mcp-protocol/schema"
    "github.com/viant/mcp-protocol/authorization"
)

func ExampleSpecBasedClient() {
    ctx := context.Background()

    // 1. Create OAuth2 config for the auth server
    oauthConfig := &oauth2.Config{
        ClientID:     "clientID",
        ClientSecret: "clientSecret",
        Endpoint: oauth2.Endpoint{
            AuthURL:  "https://auth.example.com/authorize",
            TokenURL: "https://auth.example.com/token",
        },
        Scopes: []string{"openid", "profile", "email"},
    }

    // 2. Register the config in a store for issuer discovery
    memStore := store.NewMemoryStore(store.WithClientConfig(oauthConfig))

    // 3. Create the OAuth2-enabled RoundTripper (handles metadata discovery)
    rt, err := transport.New(
        transport.WithStore(memStore),
        transport.WithAuthFlow(flow.NewBrowserFlow()),
    )
    if err != nil {
        log.Fatal(err)
    }

    // 4. Use the RoundTripper in an HTTP client
    httpClient := &http.Client{Transport: rt}

    // 5. Create an SSE transport that uses the OAuth2-enabled HTTP client
    sseTransport, err := sse.New(ctx, "https://myapp.example.com/sse", sse.WithClient(httpClient))
    if err != nil {
        log.Fatal(err)
    }

    // 6. Instantiate and initialize the MCP client (token acquisition is automatic)
    mcpClient := client.New("MyClient", "1.0", sseTransport,
        client.WithCapabilities(schema.ClientCapabilities{}),
    )
    _, err = mcpClient.Initialize(ctx)
    if err != nil {
        log.Fatal(err)
    }
}
```

## 2. Fine-Grained Tool/Resource Control (Experimental)

The experimental fine-grained mode lets you protect individual tools or resources with custom scopes or authorization servers.

### Server Side

Use `server.WithAuthConfig` and the `Tools` map in `schema.AuthConfig` to specify per-tool metadata:
```go
import (
    "context"
    "log"
    auth "github.com/viant/mcp-protocol/oauth2/auth"
    "github.com/viant/mcp/server"
    "github.com/viant/mcp/server/auth"
    "github.com/viant/mcp-protocol/oauth2/meta"
    "github.com/viant/mcp-protocol/schema"
)

func main() {
    // Map each tool name to its authorization metadata
    // Map each tool name to its authorization metadata
    toolAuth := map[string]*authorization.Authorization{
        "terminal": {
            ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
                Resource:             "http://localhost:4981",
                AuthorizationServers: []string{"https://auth.example.com/"},
            },
            // Optional: RequiredScopes: []string{"resource.read"},
        },
    }
    authCfg := &auth.Config{
        ExcludeURI: "/sse",
        Tools:      toolAuth,
    }
    srv, err := server.New(
        server.WithAuthConfig(authCfg),
        // Other server options...
    )
    if err != nil {
        log.Fatal(err)
    }
    httpSrv := srv.HTTP(context.Background(), ":4981")
    log.Fatal(httpSrv.ListenAndServe())
}
```

### Fallback Token Fetching (Optional, experimental)

If you want the server to automatically fetch and retry with fresh tokens when clients don’t supply or send expired tokens, wrap your strict `AuthServer` with `FallbackAuth`:
```go
import (
    "github.com/viant/mcp/server/auth"
    transport "github.com/viant/mcp/client/auth/transport"
    "context"
    "github.com/viant/mcp-protocol/authorization"
    meta "github.com/viant/mcp-protocol/oauth2/meta"
)

// Create the strict AuthServer
strictAuth, _ := auth.NewAuthServer(&authorization.Config{
    Global: &authorization.Authorization{ProtectedResourceMetadata: &meta.ProtectedResourceMetadata{
        Resource: "https://myapp.example.com",
        AuthorizationServers: []string{"https://auth.example.com/"},
    }},
    ExcludeURI: "/sse",
})

// Create an HTTP RoundTripper to fetch tokens (implements both ProtectedResourceTokenSource and IdTokenSource)
rt, _ := transport.New(
    transport.WithStore(store),
    transport.WithAuthFlow(flow.NewBrowserFlow()),
)

// Wrap with FallbackAuth
fallbackAuth := auth.NewFallbackAuth(strictAuth, rt, rt)

// Use the fallback authorizer in your server
srv, _ := server.New(
    server.WithAuthConfig(config),        // initializes strictAuth
    server.WithAuthorizer(fallbackAuth.EnsureAuthorized),
    // other options...
)
```

### Securing the MCP Client (experimental)

Attach the experimental RPC interceptor for 401 challenges when calling tools/resources:
```go
import (
    "context"
    "log"
    "net/http"
    "github.com/viant/jsonrpc/transport/client/http/sse"
    "github.com/viant/mcp/client"
    "github.com/viant/mcp/client/auth"
    "github.com/viant/mcp/client/auth/flow"
    "github.com/viant/mcp/client/auth/store"
    "github.com/viant/mcp/client/auth/transport"
    "github.com/viant/mcp-protocol/schema"
)

func main() {
    ctx := context.Background()

    clientConfig := &oauth2.Config{...}// OAuth2 client config
    store := store.NewMemoryStore(store.WithClientConfig(clientConfig))
    rt, err := transport.New(
        transport.WithStore(store),
        transport.WithAuthFlow(flow.NewBrowserFlow()),
    )
    if err != nil {
        log.Fatal(err)
    }
    httpClient := &http.Client{Transport: rt}
    sseTransport, _ := sse.New(ctx, "http://localhost:4981/sse", sse.WithClient(httpClient))

    // Attach the experimental interceptor
    authorizer := auth.Authorizer{Transport: rt}
    mcpClient := client.New("tester", "0.1", sseTransport,
        client.WithCapabilities(schema.ClientCapabilities{}),
        client.WithAuthInterceptor(&authorizer),
    )

    // Initialize and invoke tools; unauthorized calls will be intercepted and retried with a valid token.
    _, err = mcpClient.Initialize(ctx)
    if err != nil {
        log.Fatal(err)
    }
}
```

## Accessing Token in Implementer

When implementing MCP services, you can access the authentication token from the context in your implementer methods. The token is stored in the context using the `auth.TokenKey` key and contains an instance of the `auth.Token` struct (from `github.com/viant/mcp-protocol/oauth2/auth`).

```go
import auth "github.com/viant/mcp-protocol/oauth2/auth"

// Retrieve the token from the context
tokenValue := ctx.Value(auth.TokenKey)
if tokenValue != nil {
    // Cast to the Token struct
    authToken, ok := tokenValue.(auth.Token)
    if ok {
        // Access the token string
        tokenString := authToken.Token

        // Use the token for authorization checks, logging, etc.
       // log.Printf("Request authenticated with token: %s", tokenString)

        // You can also pass the token to other services
        // or use it to make authorized requests to other APIs
    }
}
```

### Example: Using the Token in an Implementer Method

Here's a complete example showing how to access and use the token in an implementer method:

```go
import auth "github.com/viant/mcp-protocol/authorization"

func (i *MyImplementer) CallTool(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
    // Access the token from the context
    tokenValue := ctx.Value(authorization.TokenKey)
    if tokenValue != nil {
        // You can log the token for debugging
        authToken, ok := tokenValue.(auth.Token)
        if ok {
            // Use the token for your implementation logic
            // For example, you might want to validate permissions based on the token
            if !hasPermission(authToken.Token, request.Params.Name) {
                return nil, jsonrpc.NewError(schema.Unauthorized, "Insufficient permissions", nil)
            }
        }
    }

    // Continue with the implementation...
    // ...

    return &schema.CallToolResult{
        // Result data
    }, nil
}

// Helper function to check permissions based on the token
func hasPermission(token, toolName string) bool {
    // Implement your permission logic here
    // This could involve decoding the JWT, checking claims, etc.
    return true // Simplified for example
}
```


## Securing the MCP Client

Wire OAuth2 into your MCP client transport using the provided auth packages, and attach a fine-can you grained interceptor for resource/tool-level control:
```go
import (
    "context"
    "log"
    "net/http"
    "github.com/viant/jsonrpc/transport/client/http/sse"
    "github.com/viant/mcp/client"
    "github.com/viant/mcp/client/auth"
    "github.com/viant/mcp/client/auth/flow"
    "github.com/viant/mcp/client/auth/store"
    "github.com/viant/mcp/client/auth/transport"
    "github.com/viant/mcp-protocol/schema"
)

func ExampleOAuth2Client() {
    ctx := context.Background()

    clientConfig := &oauth2.Config{...}// OAuth2 client config

// 1. Create an in-memory store with OAuth2 client credentials.
    store := store.NewMemoryStore(store.WithClient(oauthConfig))

    // 2. Build an OAuth2-enabled RoundTripper using PKCE flow.
    rt, err := transport.New(
        transport.WithStore(store),
        transport.WithAuthFlow(flow.NewBrowserFlow()),
    )
    if err != nil {
        log.Fatal(err)
    }

    // 3. Use the RoundTripper in an HTTP client.
    httpClient := &http.Client{Transport: rt}

    // 4. Create an SSE transport that uses the OAuth2-enabled HTTP client.
    sseTransport, err := sse.New(ctx, "http://myapp.example.com/sse", sse.WithClient(httpClient))
    if err != nil {
        log.Fatal(err)
    }

    // 5. Instantiate the MCP client with the SSE transport.
    //    Attach WithAuthInterceptor for handling 401 challenges at the RPC level.
    authorizer := auth.Authorizer{Transport: rt}
    mcpClient := client.New(
        "MyClient", "1.0", sseTransport,
        client.WithCapabilities(schema.ClientCapabilities{}),
        client.WithAuthInterceptor(&authorizer),
    )

    // 6. Initialize and use the client as usual; unauthorized calls will be intercepted and retried.
    _, err = mcpClient.Initialize(ctx)
    if err != nil {
        log.Fatal(err)
    }
}
```
