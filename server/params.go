package server

import "encoding/json"

func unmarshalOptionalParams(data json.RawMessage, dest interface{}) error {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, dest)
}
