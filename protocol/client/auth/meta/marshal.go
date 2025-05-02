package meta

import (
	"encoding/json"
	"reflect"
	"strings"
)

// UnmarshalJSON custom unmarshal to preserve unknown members in Extra.
func (j *JSONWebKey) UnmarshalJSON(data []byte) error {
	type alias JSONWebKey
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*j = JSONWebKey(a)

	// Capture extensions
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	known := reflect.TypeOf(a)
	for i := 0; i < known.NumField(); i++ {
		key := strings.Split(known.Field(i).Tag.Get("json"), ",")[0]
		delete(raw, key)
	}
	j.Extra = make(map[string]any, len(raw))
	for k, v := range raw {
		var vAny any
		_ = json.Unmarshal(v, &vAny)
		j.Extra[k] = vAny
	}
	return nil
}

// MarshalJSON writes Extra back out.
func (j JSONWebKey) MarshalJSON() ([]byte, error) {
	type alias JSONWebKey
	core, err := json.Marshal(alias(j))
	if err != nil {
		return nil, err
	}
	if len(j.Extra) == 0 {
		return core, nil
	}
	var base map[string]any
	_ = json.Unmarshal(core, &base)
	for k, v := range j.Extra {
		base[k] = v
	}
	return json.Marshal(base)
}
