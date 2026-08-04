package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/viant/mcp-protocol/schema"
	"io"
	"net/http"
	"strings"
)

const maxProtocolRequestBody = 4 << 20

// protocolVersionMiddleware sets the response MCP-Protocol-Version header.
// Negotiation should happen at initialize time; transport-level requests should
// not be rejected solely due to a newer client-advertised version.
func protocolVersionMiddleware() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requested := strings.TrimSpace(r.Header.Get(schema.HeaderProtocolVersion))
			if requested != "" && !supportedProtocolVersion(requested) {
				writeProtocolError(w, nil, schema.ErrorCodeUnsupportedProtocolVersion,
					fmt.Sprintf("unsupported MCP protocol version %q", requested), map[string]interface{}{
						"requested": requested,
						"supported": schema.SupportedProtocolVersions,
					})
				return
			}
			if r.Method == http.MethodPost && r.Body != nil {
				body, err := readProtocolRequestBody(w, r)
				if err != nil {
					return
				}
				if err := validateProtocolHTTPHeaders(r.Header, requested, body); err != nil {
					writeProtocolError(w, err.id, err.code, err.message, nil)
					return
				}
			}
			w.Header().Set(schema.HeaderProtocolVersion, negotiatedProtocolVersion(requested))
			next.ServeHTTP(w, r)
		})
	}
}

type protocolHeaderError struct {
	id      json.RawMessage
	code    int
	message string
}

func (e *protocolHeaderError) Error() string { return e.message }

func readProtocolRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxProtocolRequestBody+1))
	if err != nil {
		http.Error(w, "failed to read MCP request body", http.StatusBadRequest)
		return nil, err
	}
	_ = r.Body.Close()
	if len(body) > maxProtocolRequestBody {
		http.Error(w, "MCP request body is too large", http.StatusRequestEntityTooLarge)
		return nil, fmt.Errorf("MCP request body exceeds %d bytes", maxProtocolRequestBody)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	return body, nil
}

func validateProtocolHTTPHeaders(header http.Header, headerVersion string, body []byte) *protocolHeaderError {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if len(bytes.TrimSpace(body)) == 0 || json.Unmarshal(body, &request) != nil || request.Method == "" {
		return nil // JSON-RPC decoding reports malformed messages downstream.
	}
	// The July routing headers and per-request version metadata apply to calls,
	// not notifications.
	if len(request.ID) == 0 || string(request.ID) == "null" {
		return nil
	}
	metaVersion := protocolVersionFromParams(request.Params)
	usesJuly := headerVersion == schema.LatestProtocolVersion || metaVersion == schema.LatestProtocolVersion
	if !usesJuly {
		return nil
	}
	if headerVersion == "" {
		return newProtocolHeaderError(request.ID, schema.ErrorCodeHeaderMismatch,
			fmt.Sprintf("%s header is required", schema.HeaderProtocolVersion))
	}
	if metaVersion == "" {
		return newProtocolHeaderError(request.ID, -32602,
			fmt.Sprintf("missing or invalid _meta field %q", "io.modelcontextprotocol/protocolVersion"))
	}
	if headerVersion != metaVersion {
		return newProtocolHeaderError(request.ID, schema.ErrorCodeHeaderMismatch,
			fmt.Sprintf("%s header %q does not match request protocol version %q", schema.HeaderProtocolVersion, headerVersion, metaVersion))
	}
	method := strings.TrimSpace(header.Get(schema.HeaderMethod))
	if method == "" {
		return newProtocolHeaderError(request.ID, schema.ErrorCodeHeaderMismatch,
			fmt.Sprintf("missing required %s header", schema.HeaderMethod))
	}
	if method != request.Method {
		return newProtocolHeaderError(request.ID, schema.ErrorCodeHeaderMismatch,
			fmt.Sprintf("%s header %q does not match request method %q", schema.HeaderMethod, method, request.Method))
	}
	field := standardHeaderNameField(request.Method)
	if field == "" {
		return nil
	}
	name := protocolNameFromParams(request.Params, field)
	headerName := strings.TrimSpace(header.Get(schema.HeaderName))
	if headerName == "" {
		return newProtocolHeaderError(request.ID, schema.ErrorCodeHeaderMismatch,
			fmt.Sprintf("missing required %s header for method %q", schema.HeaderName, request.Method))
	}
	if name == "" || headerName != name {
		return newProtocolHeaderError(request.ID, schema.ErrorCodeHeaderMismatch,
			fmt.Sprintf("%s header %q does not match request %s %q", schema.HeaderName, headerName, field, name))
	}
	return nil
}

func newProtocolHeaderError(id json.RawMessage, code int, message string) *protocolHeaderError {
	return &protocolHeaderError{id: id, code: code, message: message}
}

func protocolVersionFromParams(raw json.RawMessage) string {
	var params struct {
		Meta map[string]interface{} `json:"_meta"`
	}
	if json.Unmarshal(raw, &params) != nil {
		return ""
	}
	version, _ := params.Meta["io.modelcontextprotocol/protocolVersion"].(string)
	return version
}

func protocolNameFromParams(raw json.RawMessage, field string) string {
	var params map[string]json.RawMessage
	if json.Unmarshal(raw, &params) != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(params[field], &value)
	return value
}

func standardHeaderNameField(method string) string {
	switch method {
	case schema.MethodToolsCall, schema.MethodPromptsGet:
		return "name"
	case schema.MethodResourcesRead:
		return "uri"
	default:
		return ""
	}
}

func writeProtocolError(w http.ResponseWriter, id json.RawMessage, code int, message string, data interface{}) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	errorValue := map[string]interface{}{"code": code, "message": message}
	if data != nil {
		errorValue["data"] = data
	}
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   errorValue,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(response)
}

func negotiatedProtocolVersion(requested string) string {
	requested = strings.TrimSpace(requested)
	switch requested {
	case "", schema.LatestProtocolVersion:
		return schema.LatestProtocolVersion
	case schema.LegacyProtocolVersion, "2025-06-18":
		return requested
	default:
		return schema.LatestProtocolVersion
	}
}
