# Opaque JSON tool arguments

MCP tool calls keep the standard `tools/call` request format. Existing handlers
continue receiving `request.Params.Arguments` with their existing decoded Go
values. No client change is required for those handlers.

Handlers that forward a JSON definition with embedded byte fingerprints can use
`server.ToolArgumentsJSON(ctx, request)` to obtain the argument encoding admitted
by the dispatcher, without decoding and re-encoding its nested objects again.
The helper returns an owned copy scoped to the exact `*schema.CallToolRequest`.
The boolean reports whether a single original argument encoding is available.
A copied request returns `nil, false`. If the dispatcher receives repeated
arguments members, Go can merge their objects, so the accessor also returns
`nil, false`. Legacy metadata normalization can collapse exact duplicate members
to the last value before dispatch. In that case both views use the last value.
Handlers that require byte fidelity must check the boolean.
The bytes belong to the dispatch context. Retaining that context also retains
the bytes, so background work should retain only the data it needs. Omitted
arguments return `nil, true`. Explicit JSON `null` and `{}` retain their distinct
encodings.

The ordinary request decoder, protocol checks, authorization and tool dispatch
still run. The raw view is data, not an authorization decision or a replacement
for the producer's content validation. Client and server metadata adapters update
`_meta` without decoding opaque argument content into generic maps. The in-process
server adapter also retains argument values explicitly supplied as
`json.RawMessage`, including their whitespace and escaping. Ordinary decoded
argument types stay unchanged. When an arguments object supplies differently
cased keys for one Go struct field, binding from raw JSON follows input order.
Binding from a re-encoded map can produce a different order. Authorize the
same resolved target that the operation will use.

This does not promise that arbitrary JSON clients preserve whitespace or escaping
before transmission. Fingerprints must describe the encoding actually supplied;
handlers editing a definition must preserve its untouched embedded content.

Executable examples and regressions live in `server/tool_arguments_test.go`,
`server/tool_arguments_http_test.go` and `client/numeric_metadata_test.go`.
