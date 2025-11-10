# OOB Pending Primitives

Minimal, generic primitives to manage out‑of‑band (OOB) interactions with strong namespace remapping and no opinions about UI. You define device pages, forms, or redirects; this package ensures every callback maps back to the correct namespace.

## What It Provides
- Generic, typed `Store[T]`: source of truth for `id → namespace` (+ typed payload).
- `Manager[T]`: resolves namespace on create, stores pending, builds a callback URL, and returns it.
- HTTP helper `NamespaceFromPending[T]`: loads pending by id and injects the correct namespace into context before running your handler.

## What It Doesn’t Do
- No UI or templates, no device page HTML, no OAuth redirect logic. Those stay in your service.

## Types
- `Pending[T]`: typed entry bound to a namespace. Fields: `ID, Namespace, Kind, Alias, Resource, ElicitID, CreatedAt, ExpiresAt, Data T`.
- `Spec[T]`: inputs to create a pending (same minus ids/timestamps).
- `Store[T]`: generic interface: `Put`, `Get`, `Complete`, `Cancel`, `ListNamespace`, `ClearNamespace`.
- `Manager[T]`: `Create(ctx, spec) (id, callbackURL)`, `Complete(ctx, id)`, `Cancel(ctx, id)`.
- `NamespaceFromPending[T]`: HTTP wrapper that injects the correct `namespace.Descriptor` and the loaded `Pending[T]` into context.

A simple in‑memory `MemoryStore[T]` is included.

## Typical Flow
1) Tool detects missing credentials and starts an OOB interaction
- `id, callbackURL, err := mgr.Create(ctx, Spec[T]{Kind: kind, Alias: alias, Resource: resource, Data: payload})`
- If client supports MCP Elicit, you can send `ElicitID = id` and include `callbackURL` in the message.

2) Service registers an interaction route and renders OOB UI
- `mux.Handle("/service/auth/interaction/", oob.NamespaceFromPending(store, extractID, yourHandler))`
- `yourHandler(ctx, pending, w, r)` fully controls presentation (device code, form, redirect).

3) On success, complete and persist under the correct namespace
- `pending, err := mgr.Complete(ctx, id)` // returns the pending with `Namespace`
- Save credentials/secrets under `pending.Namespace` and activate resources only for that namespace.

## Example: Device Code (Typed Payload)
```go
// Define a payload type for your service
type DevicePayload struct {
    VerifyURL string
    UserCode  string
    Message   string
}

store := oob.NewMemoryStore[DevicePayload]()
mgr := &oob.Manager[DevicePayload]{
    Provider:        nsProvider, // github.com/viant/mcp/server/namespace.Provider
    Store:           store,
    CallbackBuilder: func(id string) string { return "/service/auth/interaction/" + id },
}

// When auth is needed
id, callbackURL, _ := mgr.Create(ctx, oob.Spec[DevicePayload]{
    Kind:     "device_code",
    Alias:    alias,
    Resource: "github.com",
    Data:     DevicePayload{VerifyURL: vURL, UserCode: code, Message: msg},
})
// Optionally send an MCP Elicit with ElicitID=id and a link to callbackURL

// Interaction route
extractID := func(r *http.Request) (string, error) { parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/"); return parts[len(parts)-1], nil }
mux.Handle("/service/auth/interaction/", oob.NamespaceFromPending(store, extractID, func(ctx context.Context, p oob.Pending[DevicePayload], w http.ResponseWriter, r *http.Request) error {
    // p.Namespace is correct; namespace.Descriptor is injected into ctx
    // Render your device page using p.Data.VerifyURL and p.Data.UserCode
    // On completion (e.g., after polling), call mgr.Complete(ctx, p.ID)
    return nil
}))
```

## Example: Secret Form (SQL‑style)
```go
type SecretForm struct { RequestedSchema any; Title string }
store := oob.NewMemoryStore[SecretForm]()
mgr := &oob.Manager[SecretForm]{ Provider: nsProvider, Store: store, CallbackBuilder: func(id string) string { return "/db/auth/interaction/"+id } }

id, callbackURL, _ := mgr.Create(ctx, oob.Spec[SecretForm]{ Kind: "basic_credentials", Alias: connectorName, Resource: driverDB, Data: SecretForm{RequestedSchema: schema} })
// If MCP Elicit is available, send a message linking to callbackURL

mux.Handle("/db/auth/interaction/", oob.NamespaceFromPending(store, extractID, func(ctx context.Context, p oob.Pending[SecretForm], w http.ResponseWriter, r *http.Request) error {
    // Render a form based on p.Data.RequestedSchema, submit to this URL
    // On submit success: pend, _ := mgr.Complete(ctx, p.ID); save under pend.Namespace
    return nil
}))
```

## Notes
- Namespace is derived once at create time from the request token and stored with the pending; all callbacks map back to it by id.
- `NamespaceFromPending` injects a minimal `Descriptor{Name: pending.Namespace}`; if you need FS‑friendly pathing, compute it in your service using the same policy you use for namespace path derivation.
- Include TTLs in your store or validate `ExpiresAt` to avoid stale interactions.
- You can add dedup/waiters separately if your service needs them to prevent prompt storms or to block until completion.
