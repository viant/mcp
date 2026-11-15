# Opaque JSON tool arguments

MCP tool calls keep the standard `tools/call` request format. Existing handlers
continue receiving `request.Params.Arguments` with their existing decoded Go
values. No client change is required for those handlers.

Handlers that forward a JSON definition with embedded byte fingerprints can use
`server.ToolArgumentsJSON(ctx, request)` to obtain the argument encoding admitted
by the dispatcher, without decoding and re-encoding its nested objects again.
The helper returns an owned copy scoped to the exact `*schema.CallToolRequest`.
It returns `false` outside that invocation. Omitted arguments return `nil, true`;
explicit JSON `null` and `{}` retain their distinct encodings.

The ordinary request decoder, protocol checks, authorization and tool dispatch
still run. The raw view is data, not an authorization decision or a replacement
for the producer's content validation. Client and server metadata adapters update
`_meta` without decoding opaque argument content into generic maps.

This does not promise that arbitrary JSON clients preserve whitespace or escaping
before transmission. Fingerprints must describe the encoding actually supplied;
handlers editing a definition must preserve its untouched embedded content.

Executable examples and regressions live in `server/tool_arguments_test.go`,
`server/tool_arguments_http_test.go` and `client/numeric_metadata_test.go`.
