package schema

import "encoding/json"

// CallToolRequestParams is a structure that holds the parameters for a tool call request.
func NewCallToolRequestParams[T any](name string, cmd *T) (*CallToolRequestParams, error) {
	results := &CallToolRequestParams{Name: name, Arguments: map[string]interface{}{}}
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &results.Arguments)
	if err != nil {
		return nil, err
	}
	return results, nil
}
