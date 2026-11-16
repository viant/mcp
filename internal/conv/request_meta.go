package conv

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/sjson"
	"github.com/viant/jsonrpc"
	"github.com/viant/mcp-protocol/schema"
)

// SetRequestMeta updates a request using its already decoded, invocation-local
// params. Tool arguments retain their original encoding, including when _meta
// was absent. The request is replaced only after successful serialization.
func SetRequestMeta(req *jsonrpc.Request, params map[string]json.RawMessage, meta map[string]any) error {
	encodedMeta, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	params["_meta"] = encodedMeta
	var argumentKey string
	if req.Method == schema.MethodToolsCall {
		for key := range params {
			if strings.EqualFold(key, "arguments") {
				if argumentKey != "" {
					// Keep normal map encoding order when multiple spellings coexist.
					argumentKey = ""
					break
				}
				argumentKey = key
			}
		}
	}
	arguments := params[argumentKey]
	if argumentKey != "" {
		delete(params, argumentKey)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if argumentKey != "" {
		raw, err = sjson.SetRawBytes(raw, argumentKey, arguments)
		if err != nil {
			return err
		}
	}
	req.Params = raw
	return nil
}
