# MCP Client Guide (Go)

This guide shows how to create an MCP client in Go, connect via different transports (SSE, streamable, stdio), optionally enable OAuth2/OIDC, and perform common calls like listing resources, calling tools, and working with prompts.

## Transports

### SSE

```go
package main

import (
    "context"
    "fmt"
    "log"

    mcpclient "github.com/viant/mcp/client"
    "github.com/viant/mcp-protocol/schema"
    sse "github.com/viant/jsonrpc/transport/client/http/sse"
)

func main() {
    ctx := context.Background()
    transport, err := sse.New(ctx, "http://localhost:4981/sse")
    if err != nil { log.Fatal(err) }

    cli := mcpclient.New("Demo", "1.0", transport)
    if _, err := cli.Initialize(ctx); err != nil { log.Fatal(err) }

    res, _ := cli.ListResources(ctx, nil)
    fmt.Println("resources:", len(res.Resources))
}
```

### HTTP Streamable

```go
import (
    streamable "github.com/viant/jsonrpc/transport/client/http/streamable"
)

transport, _ := streamable.New(ctx, "http://localhost:4981/")
cli := mcpclient.New("Demo", "1.0", transport)
_, _ = cli.Initialize(ctx)
```

### Stdio (spawn a child process)

```go
import (
    stdio "github.com/viant/jsonrpc/transport/client/stdio"
)

transport, _ := stdio.New("./your-mcp-server-binary",
    stdio.WithArguments("--flag1", "value"))
cli := mcpclient.New("Demo", "1.0", transport)
_, _ = cli.Initialize(ctx)
```

## OAuth2 / OIDC (optional)

When an MCP server requires OAuth2/OIDC, build an authenticated `http.Client` using the helper round-tripper and pass it to the SSE/streamable transport.

```go
import (
    "net/http"
    authflow "github.com/viant/scy/auth/flow"
    authstore "github.com/viant/mcp/client/auth/store"
    authrt "github.com/viant/mcp/client/auth/transport"
)

// Memory store keeps OAuth client config and tokens
store := authstore.NewMemoryStore()
rt, err := authrt.New(
    authrt.WithStore(store),
    authrt.WithAuthFlow(authflow.NewBrowserFlow()),
)
if err != nil { log.Fatal(err) }
httpClient := &http.Client{Transport: rt}

// SSE with authenticated client
transport, _ := sse.New(ctx, "https://secure.example.com/sse", sse.WithHttpClient(httpClient))
cli := mcpclient.New("Demo", "1.0", transport)
_, _ = cli.Initialize(ctx)
```

 Tip: The interceptor automatically handles 401 challenges, discovers protected-resource metadata, acquires tokens, and retries.

## High-Level Helper (options + reconnect + auth)

The root package provides a convenience helper that builds transports and wires an auth interceptor. Use this when you have user-facing features like sampling/elicitation (server-initiated calls) and want automatic reconnect.

```go
import (
    mcp "github.com/viant/mcp"
    pclient "github.com/viant/mcp-protocol/client"
)

// Implement pclient.Handler only if your client must handle server-initiated methods
var handler pclient.Handler = pclient.NewDefault()

cli, err := mcp.NewClient(handler, &mcp.ClientOptions{
    Name:    "Demo",
    Version: "1.0",
    Transport: mcp.ClientTransport{
        Type: "sse", // or "streamable" or "stdio"
        ClientTransportHTTP: mcp.ClientTransportHTTP{URL: "http://localhost:4981/sse"},
        // ClientTransportStdio: mcp.ClientTransportStdio{Command: "./serverbin"},
    },
    Auth: &mcp.ClientAuth{
        OAuth2ConfigURL: []string{"file://oauth_client.json"},
        UseIdToken:      false,
    },
})
if err != nil { log.Fatal(err) }
// Initialize is performed automatically by NewClient
```

## Common Calls

Assuming `cli` is an initialized `*client.Client` and `ctx := context.Background()`:

- Initialize:

```go
_, err := cli.Initialize(ctx)
```

- List resources:

```go
lr, err := cli.ListResources(ctx, nil)
for _, r := range lr.Resources { fmt.Println(r.Uri) }
```

- Read resource:

```go
params := &schema.ReadResourceRequestParams{Uri: "file://path/to/file.txt"}
rr, err := cli.ReadResource(ctx, params)
fmt.Println(rr.Contents[0].Text)
```

- List tools:

```go
lt, err := cli.ListTools(ctx, nil)
for _, t := range lt.Tools { fmt.Println(t.Name) }
```

- Call tool (typed params helper):

```go
type AddIn struct{ A, B int }
callParams, _ := schema.NewCallToolRequestParams("add", &AddIn{A: 2, B: 3})
res, err := cli.CallTool(ctx, callParams)
fmt.Println(res.Content[0].Text)
```

- List prompts and get a prompt:

```go
lp, _ := cli.ListPrompts(ctx, nil)
for _, p := range lp.Prompts { fmt.Println(p.Name) }

pg, _ := cli.GetPrompt(ctx, &schema.GetPromptRequestParams{
    Name:      "welcome",
    Arguments: map[string]string{"name": "Alice"},
})
for _, m := range pg.Messages {
    fmt.Printf("%s: %v\n", m.Role, m.Content)
}
```

- Subscribe/unsubscribe:

```go
_, _ = cli.Subscribe(ctx, &schema.SubscribeRequestParams{Uri: "/hello"})
// Handle notifications via your transport’s notification hook if needed
_, _ = cli.Unsubscribe(ctx, &schema.UnsubscribeRequestParams{Uri: "/hello"})
```

- Completion (if server implements it):

```go
comp, _ := cli.Complete(ctx, &schema.CompleteRequestParams{Prompt: "Say hi"})
fmt.Println(comp.Content)
```

## Notes

- Initialize once per transport session; the client will cache capabilities and metadata.
- For stdio, attach `_meta.authorization.token` automatically using request options if needed (see client.WithAuthInterceptor for HTTP; for stdio use `WithAuthToken`).
- If the server restarts, the client auto-reconnects when created with the high-level helper. Otherwise, handle errors and recreate the transport.

---

References:
- Client: `github.com/viant/mcp/client`
- Protocol schema: `github.com/viant/mcp-protocol/schema`
- Transports: `github.com/viant/jsonrpc/transport/client/...`
- Auth: `github.com/viant/mcp/client/auth/transport`
