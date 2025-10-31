# MCP Server Namespace

Namespace provides a reusable, policy-aware way to derive a per-request service namespace from the OAuth token carried in context. It centralizes identity-vs-hash selection, supplies a filesystem-friendly path prefix, and offers a generic resolver to route MCP tools to a service instance already bound to the caller’s namespace.

## Why
- Consistent isolation: Prevent cross-user data leakage by scoping services per identity or token.
- Shared behavior: Remove duplicated "namespace from token" code across MCP services.
- FS-friendly paths: Produce safe `PathPrefix`/`ShardedPath` for namespaced storage.
- Clean routing: Resolve once at the tool boundary and work with a namespace-bound service instance.

## How It Works
- Input: Authorization token is placed in `context` under `authorization.TokenKey` by `server/auth` middleware.
- Identity-first (optional): If `PreferIdentity` is true, namespace derives from ID token claims (`email` or `sub`) when present.
- Fallback: If identity isn’t available, a stable token hash (MD5 by default) is used to isolate requests.
- Default: When no token is present, default namespace (`default`) is used for local/stdio flows.
- Paths: A `Descriptor` adds `PathPrefix` and `ShardedPath` for filesystem usage, with configurable sanitization, truncation, and sharding.

## Key Types
- `Provider`
  - `Namespace(ctx) (Descriptor, error)`
  - Computes a `Descriptor` from the token in context.
- `Descriptor`
  - `Name`, `Kind` (`default | identity | token-hash`), `IsDefault`, `Hash`
  - `PathPrefix` (FS-safe single segment), `ShardedPath` (optional multi-segment)
- `Config`
  - `Default`, `PreferIdentity`, `ClaimKeys`, `Hash` (`HashConfig`), `Path` (`PathConfig`)
- `NamespaceService[T]`
  - `Resolve(ctx) (T, error)`: returns a service instance scoped to the resolved namespace
  - `WithContext(ctx) (context.Context, error)`: injects the resolved `Descriptor` into context
  - `Descriptor(ctx) (Descriptor, error)`: returns the resolved `Descriptor` without modifying context
  - `ForNamespace(ns) (T, error)`: direct access for admin flows
- Context helpers
  - `IntoContext(ctx, desc) context.Context`, `FromContext(ctx) (Descriptor, bool)`
- Extensibility
  - `WithClaimsVerifier(...)`: use verified ID token claims
  - `WithClaimsParser(...)`: custom unverified parser (default uses `jwt` unverified parse)

## Default Behavior
- Default namespace: `default` when token missing.
- Identity-first mode: `email` → `sub` claim order (configurable).
- Fallback hash: MD5 (configurable to SHA-256), optional prefix (e.g., `tkn-`), no raw token is persisted.
- Path prefix: Lowercased, `@` → `_at_`, only `[a-z0-9_.-]` retained, others replaced with `-`, optional `MaxLen` truncation with short hash suffix.
- Sharding: Optional `ShardLevels` × `ShardWidth` bytes of hash to spread directories.

## Step-by-Step Integration

### 1) Add dependency and imports
- Import: `github.com/viant/mcp/server/namespace`
- Ensure your server includes the HTTP auth middleware (`github.com/viant/mcp/server/auth`) so tokens are put into context.

### 2) Configure a Provider
- Map your auth policy to `namespace.Config`.
- Example mappings:
  - `PreferIdentity = true` when you require ID tokens (e.g., sqlkit’s `RequireIdentityToken`).
  - `Hash.Algorithm = "md5"` (default) or `"sha256"` if you want stronger hashing.
  - `Path.MaxLen`, `Path.Sanitize`, `Path.ShardLevels`, `Path.ShardWidth` as appropriate for your filesystem.
- Optionally supply a verified-claims provider with `WithClaimsVerifier(...)`.

### 3) Wrap your domain service with `NamespaceService`
- The factory must return a service instance that is namespace-agnostic. The resolver enforces isolation by returning a distinct instance per namespace.

### 4) Route at MCP tool boundary
- If you need filesystem paths, first call `WithContext(ctx)` and bubble errors.
- Then call `Resolve(ctx)` to get `service` and invoke operations.

### 5) Keep caches internal to the service
- Do not keep caches in the wrapper. Any client pools, connectors, or resource caches should be encapsulated by the namespace-bound service instance to avoid leakage.

## Example Wiring (Conceptual)

1) Provider configuration
- Prefer identity only when you expect ID tokens; otherwise fallback hash ensures isolation for access tokens.

2) Resolver
- Build `NamespaceService` with `Provider` and a `Factory` that constructs a namespace-bound instance of your domain service.

3) Handler usage
- `ctx, err := resolver.WithContext(ctx)` (bubble error)
- `service, err := resolver.Resolve(ctx)`
- `desc, _ := namespace.FromContext(ctx)` to get `PathPrefix` / `ShardedPath` when storing files.

## MCP Tool Integration Example

Below is a concrete pattern you can adapt inside your handler setup to ensure each tool call is routed to a namespace-bound service. This expands your snippet sketch into a working flow using `NamespaceService`.

1) Build and keep the resolver on your handler (once at init):

```go
type Handler struct {
    // Domain service factory returns a service already bound to a namespace.
    // For example, it can wire clients and caches scoped to the namespace inside the service.
    serviceResolver *namespace.NamespaceService[*repositoryservice.Service, *repositoryservice.ListRepositoriesInput]
}

func NewHandler(config *Config) (*Handler, error) {
    // Configure the provider; PreferIdentity=true if you require ID tokens.
    provider := namespace.NewProvider(&namespace.Config{
        Default:        "default",
        PreferIdentity: config.RequireIDToken, // map from your policy
        ClaimKeys:      []string{"email","sub"},
        Hash:           namespace.HashConfig{Algorithm: "md5", Prefix: "tkn-", Truncate: 64},
        Path:           namespace.PathConfig{Sanitize: true, MaxLen: 120, ShardLevels: 2, ShardWidth: 2},
    })

    // Factory that constructs a namespace-bound GitHub service instance
    // (internally manages any per-namespace caches/clients/lifecycle).
    factory := func(namespaceName string) (*repositoryservice.Service, error) {
        return repositoryservice.NewBound(namespaceName, config) // your constructor that binds namespace internally
    }

    return &Handler{
        serviceResolver: &namespace.NamespaceService[*repositoryservice.Service, *repositoryservice.ListRepositoriesInput]{
            Provider: provider,
            Factory:  factory,
            // Optional: Override: func(input *repositoryservice.ListRepositoriesInput) (string, bool) { return input.Namespace, input.Namespace != "" },
            // Optional: Inject: custom context injection; defaults to namespace.IntoContext
        },
    }, nil
}
```

2) Register tools and resolve a namespaced service on each call:

```go
func registerTools(base *protoserver.DefaultHandler, h *Handler) error {
    // List repositories tool
    if err := protoserver.RegisterTool[*repositoryservice.ListRepositoriesInput, *repositoryservice.ListRepositoriesOutput](
        base.Registry,
        "ListRepositories",
        "List repositories visible to the caller",
        func(ctx context.Context, input *repositoryservice.ListRepositoriesInput) (*schema.CallToolResult, *jsonrpc.Error) {
            // Resolve namespace and obtain a namespace-bound service instance.
            contextWithNamespace, service, err := h.serviceResolver.Resolve(ctx, input)
            if err != nil {
                return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, "resolve namespace: %v", err)
            }

            // Optional: derive filesystem-friendly pathing for any per-ns storage.
            if descriptor, ok := namespace.FromContext(contextWithNamespace); ok {
                _ = descriptor.PathPrefix   // e.g., use as directory prefix
                _ = descriptor.ShardedPath  // e.g., for sharded storage layout
            }

            output, serviceError := service.ListRepositories(contextWithNamespace, input)
            if serviceError != nil {
                return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, serviceError.Error())
            }

            return schema.Output(output)
        },
    ); err != nil {
        return err
    }

    // ... register other tools similarly, reusing the same resolver or another per domain
    return nil
}
```

Notes:
- Each `Resolve` call returns a context with the `Descriptor` injected; downstream can read it via `namespace.FromContext`.
- The service instance returned is already bound to the resolved namespace and should encapsulate any caches or client pools.
- If you need different requests to select different resolvers (e.g., per tool), keep one resolver per domain service on the handler.

### One Resolver For Many Operations

For services that expose many operations (e.g., 10–20 tools), define a single resolver per domain service. The resolver returns a namespace-scoped service instance; you reuse it across all tool handlers.

```go
type Handler struct {
    serviceResolver *namespace.NamespaceService[*repositoryservice.Service]
}

func NewHandler(config *Config) (*Handler, error) {
    provider := namespace.NewProvider(&namespace.Config{ /* ... */ })
    factory := func() (*repositoryservice.Service, error) {
        // Construct a service instance without any namespace knowledge.
        // Isolation is provided by the resolver returning a distinct instance per namespace.
        return repositoryservice.New(config)
    }
    return &Handler{
        serviceResolver: &namespace.NamespaceService[*repositoryservice.Service]{
            Provider: provider,
            Factory:  factory,
        },
    }, nil
}

func registerTools(base *protoserver.DefaultHandler, h *Handler) error {
    // Tool A: ListRepositories
    if err := protoserver.RegisterTool[*repositoryservice.ListRepositoriesInput, *repositoryservice.ListRepositoriesOutput](
        base.Registry, "ListRepositories", "List repositories", func(ctx context.Context, input *repositoryservice.ListRepositoriesInput) (*schema.CallToolResult, *jsonrpc.Error) {
            // If you need filesystem paths, inject Descriptor and bubble errors
            ctxWithNamespace, err := h.serviceResolver.WithContext(ctx)
            if err != nil { return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, "resolve namespace: %v", err) }
            ctx = ctxWithNamespace

            service, err := h.serviceResolver.Resolve(ctx)
            if err != nil { return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, "resolve namespace: %v", err) }
            output, serviceError := service.ListRepositories(ctx, input)
            if serviceError != nil { return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, serviceError.Error()) }
            return schema.Output(output)
        }); err != nil { return err }

    // Tool B: CreateRepository (reuse the same resolver)
    if err := protoserver.RegisterTool[*repositoryservice.CreateRepositoryInput, *repositoryservice.CreateRepositoryOutput](
        base.Registry, "CreateRepository", "Create a repository", func(ctx context.Context, input *repositoryservice.CreateRepositoryInput) (*schema.CallToolResult, *jsonrpc.Error) {
            service, err := h.serviceResolver.Resolve(ctx)
            if err != nil { return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, "resolve namespace: %v", err) }
            output, serviceError := service.CreateRepository(ctx, input)
            if serviceError != nil { return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, serviceError.Error()) }
            return schema.Output(output)
        }); err != nil { return err }

    // Add more repository tools here, reusing the same resolver
    return nil
}
```

Notes:
- `Resolve(ctx)` returns a namespace-scoped instance; the service does not receive the namespace and remains unaware of isolation.
- Call `WithContext(ctx)` if you want to inject the `Descriptor` (for `PathPrefix` / `ShardedPath`) before invoking service methods.

## Another Example: SQL Query Tool (mcp-sqlkit style)

This example shows integrating a SQL query tool where the service is already scoped to a namespace and may use the descriptor’s `PathPrefix`/`ShardedPath` for optional per-namespace working directories (e.g., temp files, query plans, or logs).

```go
// sqlhandler.go
type SQLHandler struct {
    databaseResolver *namespace.NamespaceService[*sqlservice.Service]
}

func NewSQLHandler(config *Config) (*SQLHandler, error) {
    provider := namespace.NewProvider(&namespace.Config{
        Default:        "default",
        PreferIdentity: config.RequireIDToken,        // typically true when ID tokens are required
        ClaimKeys:      []string{"email", "sub"},   // derive from identity first when available
        Hash:           namespace.HashConfig{Algorithm: "md5", Prefix: "tkn-", Truncate: 64},
        Path:           namespace.PathConfig{Sanitize: true, MaxLen: 120, ShardLevels: 2, ShardWidth: 2},
    })

    factory := func() (*sqlservice.Service, error) {
        // Construct a service instance without any namespace knowledge.
        // Isolation is provided by the resolver returning a distinct instance per namespace.
        return sqlservice.New(config)
    }

    return &SQLHandler{
        databaseResolver: &namespace.NamespaceService[*sqlservice.Service]{
            Provider: provider,
            Factory:  factory,
        },
    }, nil
}

func registerSQLTools(base *protoserver.DefaultHandler, h *SQLHandler) error {
    // dbQuery tool
    if err := protoserver.RegisterTool[*query.Input, *query.Output](
        base.Registry,
        "dbQuery",
        "Run a SQL query against a configured connection",
        func(ctx context.Context, in *query.Input) (*schema.CallToolResult, *jsonrpc.Error) {
            // Inject Descriptor into context and bubble errors
            ctxWithNamespace, err := h.databaseResolver.WithContext(ctx)
            if err != nil { return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, "resolve namespace: %v", err) }
            ctx = ctxWithNamespace

            service, err := h.databaseResolver.Resolve(ctx)
            if err != nil { return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, "resolve namespace: %v", err) }

            // Example: derive a working directory using the Descriptor
            if descriptor, ok := namespace.FromContext(ctx); ok {
                workingDirectory := filepath.Join(h.config.WorkRoot, descriptor.ShardedPath)
                _ = workingDirectory
            }

            output, serviceError := service.Query(ctx, in)
            if serviceError != nil {
                return nil, jsonrpc.Errorf(jsonrpc.ErrCodeInternal, serviceError.Error())
            }
            return schema.Output(output)
        },
    ); err != nil {
        return err
    }

    // dbExec tool could be registered similarly using the same resolver.
    return nil
}
```

Highlights:
- `NamespaceService.Resolve` handles routing and context injection; the SQL service instance is already namespace-bound.
- The service keeps any caches or connections internal and keyed by the namespace.
- You can read `ShardedPath` to create a directory layout that scales for many users.

## Repo Integration Notes

### mcp-sqlkit
- Set `PreferIdentity = policy.RequireIdentityToken`.
- Use `NamespaceService` in MCP tool handlers (query/exec/connector ops) to obtain a namespace-bound service.
- Keep connector/client caches inside the service bound to ns.
- Benefit: one resolution point, consistent per-user isolation, FS paths available if needed.

### mcp-toolbox (e.g., Jira/GitHub)
- Default to unverified claims parsing + hash fallback; enable `PreferIdentity` only when ID tokens are provided.
- Wrap Jira/GitHub services with `NamespaceService` and resolve in MCP tool handlers.
- Keep SDK client pools internally per ns.

## Backwards Compatibility
- Local/stdio flows without tokens remain in `default` namespace.
- Identity extraction uses unverified parsing by default; you can inject a verifier for stricter environments.
- Token hash fallback maintains strong isolation even when only access tokens are available.

## Tips
- Use a short `Hash.Truncate` (e.g., 32 for MD5, 16 for suffixes) for readable paths.
- Set `Path.ShardLevels`/`ShardWidth` for directories with many entries.
- Do not rely on raw token data in any file paths or keys.

---

If you need a brief example aligned to your service structure, add a doc snippet near your handlers showing the `Provider` and `NamespaceService` wiring you chose.
