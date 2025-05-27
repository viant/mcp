# MCP Bridge

`mcpb` ("MCP **B**ridge") is a small helper utility that creates a local MCP endpoint (HTTP/SSE or stdio) and transparently **bridges** every request to a remote MCP server.  
Typical use-cases:

1. Building desktop / CLI applications that speak MCP over **stdio** (e.g. VS Code, Neovim, JetBrains plugins).  
   The bridge runs as a child process and forwards requests to a remote HTTP/SSE server, taking care of authentication for you.
2. Running several local tools that expect an MCP server but don’t need to host their own implementation.  
   Start the bridge once and point the tools to `localhost`.

The binary is located in `bridge/` and is published as an asset in every release for **darwin/amd64**, **darwin/arm64** and **linux/amd64**.

## Building from source

```
$ go run ./bridge -h    # for usage

# or compile a static binary
$ cd bridge && go build -o mcpb .
```

## Command-line flags

| Flag | Description | Required |
|------|-------------|----------|
| `-u`, `--url` | Remote MCP **SSE** endpoint, e.g. `https://example.com/sse` | yes |
| `-c`, `--config` | Path/URL to an OAuth2 client configuration JSON (see Authentication guide) | no |
| `-k`, `--key` | Optional AES-256 key used to decrypt the OAuth2 config | no |
| `-i`, `--id-token` | Include an **ID token** instead of (or in addition to) an access token when calling the remote endpoint | no |
| `-b`, `--backend-for-frontend` | Use [RFC-9457 Backend-for-Frontend] authentication flow. The bridge will obtain tokens on behalf of the caller. | no |
| `-h`, `--backend-for-frontend-header` | Name of the HTTP header that carries the upstream token when `--backend-for-frontend` is set | no |

Run `mcpb --help` for an up-to-date list.

## Usage examples

### 1. Stdio (default)

```
$ ./mcpb -u https://mcp.example.com/sse

# The process now listens on stdin/stdout for JSON-RPC messages.
# Anything sent to it is forwarded to https://mcp.example.com/sse.
```

Stdio mode is handy when the parent process (IDE plugin, TUI, etc.) prefers to communicate over pipes instead of sockets.

### 2. Expose a local HTTP/SSE server

```go
ctx := context.Background()
svc, _ := mcp.New(ctx, &mcp.Options{URL: "https://mcp.example.com/sse"})
httpSrv, _ := svc.HTTP(ctx, ":4981")
log.Fatal(httpSrv.ListenAndServe())
# ↑ Forward all traffic from http://localhost:4981/ to the remote server
```

### 3. OAuth2 / OIDC-protected upstream

If the remote MCP server is protected by OAuth2/OIDC, supply the client configuration:

```
$ ./mcpb -u https://secure.example.com/sse \
         -c ./oauth_client.json \
         -i                 # also include ID token
```

`oauth_client.json` must contain a public client (PKCE) definition that is compatible with the Identity Provider.  The bridge opens the default browser when user interaction is required.

For more details see the Authentication guide.

## Programmatic API

The bridge functionality can also be embedded in your Go program:

```go
svc, _ := mcp.New(context.Background(), &mcp.Options{
    URL: "https://mcp.example.com/sse",
})
stdioSrv, _ := svc.Stdio(context.Background())
_ = stdioSrv.ListenAndServe()
```

## Release automation

`bridge/build.yaml` contains a simple build pipeline that cross-compiles the binary for the supported platforms and packages them as `tar.gz` archives.  Invoked by CI on every tag push.

---

Need help?  Join the discussion in `#mcp` on the Viant Slack or open an issue on GitHub.
