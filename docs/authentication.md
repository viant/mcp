<!-- Automatically generated. Authentication and authorization guide for MCP. -->
# Authentication Guide

MCP supports securing both the server and client using OAuth2/OIDC standards, including the Authorization Code Flow with PKCE.

## OAuth 2.1 Authorization Code Flow with PKCE

1. **Initial Unauthorized Request**: The client requests an MCP resource without a token and receives `401 Unauthorized` with a `WWW-Authenticate` header pointing to metadata at `/.well-known/oauth-protected-resource`.
2. **Resource Metadata Discovery**: The client fetches `/.well-known/oauth-protected-resource` to learn which authorization servers are trusted.
3. **Authorization Server Metadata Discovery**: Using the metadata, the client fetches `/.well-known/oauth-authorization-server` to obtain the auth and token endpoints.
4. **User Login with PKCE**: The client redirects the user to log in, using the PKCE-enhanced Authorization Code flow.
5. **Token Request and Response**: After user approval, the client exchanges the code for an access token.
6. **Authorized Request to MCP**: The client retries the MCP request with a `Bearer <token>` in the `Authorization` header.
7. **Resource Response**: If the token is valid, the MCP server responds with the requested data.

## Securing the MCP Server

Use `server.WithAuthConfig` when creating your MCP server to enable OAuth2 protection:
```go
import (
    "context"
    "github.com/viant/mcp/protocol/server"
    "github.com/viant/mcp/protocol/server/auth"
    "github.com/viant/mcp/protocol/client/auth/meta"
    "github.com/viant/mcp/schema"
)

func main() {
    options := []server.Option{
        server.WithAuthConfig(&auth.Config{
            ExcludeURI: "/sse", // allow initial SSE GET (cannot refresh token on GET)
            Global: &meta.ProtectedResourceMetadata{
                Resource:             "https://myapp.example.com",
                AuthorizationServers: []string{"https://auth.example.com/"},
            },
        }),
        // other server options (implementer, capabilities, implementation info)
    }
    srv, err := server.New(options...)
    if err != nil {
        log.Fatal(err)
    }
    httpSrv := srv.HTTP(context.Background(), ":8080")
    log.Fatal(httpSrv.ListenAndServe())
}
```

This configuration will:
- Expose the OAuth2 metadata endpoint at `/.well-known/oauth-protected-resource`.
- Protect all other endpoints (e.g., `/sse`, `/`) by requiring a valid Bearer token unless ExcludeURI is specified.


## Accessing Token in Implementer

When implementing MCP services, you can access the authentication token from the context in your implementer methods. The token is stored in the context using the `schema.AuthTokenKey` key and contains an instance of the `schema.AuthToken` struct.

```go
// Retrieve the token from the context
tokenValue := ctx.Value(schema.AuthTokenKey)
if tokenValue != nil {
    // Cast to the AuthToken struct
    authToken, ok := tokenValue.(schema.AuthToken)
    if ok {
        // Access the token string
        tokenString := authToken.Token

        // Use the token for authorization checks, logging, etc.
        log.Printf("Request authenticated with token: %s", tokenString)

        // You can also pass the token to other services
        // or use it to make authorized requests to other APIs
    }
}
```

### Example: Using the Token in an Implementer Method

Here's a complete example showing how to access and use the token in an implementer method:

```go
func (i *MyImplementer) CallTool(ctx context.Context, request *schema.CallToolRequest) (*schema.CallToolResult, *jsonrpc.Error) {
    // Access the token from the context
    tokenValue := ctx.Value(schema.AuthTokenKey)
    if tokenValue != nil {
        // You can log the token for debugging
        authToken, ok := tokenValue.(schema.AuthToken)
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

Wire OAuth2 into your MCP client transport using the provided auth packages:
```go
import (
    "context"
    "net/http"
    "github.com/viant/jsonrpc/transport/client/http/sse"
    "github.com/viant/mcp/protocol/client"
    "github.com/viant/mcp/protocol/client/auth/flow"
    "github.com/viant/mcp/protocol/client/auth/store"
    "github.com/viant/mcp/protocol/client/auth/transport"
    "github.com/viant/mcp/schema"
)

func ExampleOAuth2Client() {
    ctx := context.Background()

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
    mcpClient := client.New("MyClient", "1.0", sseTransport, client.WithCapabilities(schema.ClientCapabilities{}))

    // 6. Use the client as usual; token acquisition is handled automatically.
    _, err = mcpClient.Initialize(ctx)
    if err != nil {
        log.Fatal(err)
    }
}
```
