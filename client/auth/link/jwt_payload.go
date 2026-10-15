// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

import (
	"encoding/base64"
	"encoding/json"
)

// decodeJWTSegment base64-decodes a JWT segment and returns its claims.
func decodeJWTSegment(seg string) (map[string]interface{}, error) {
	switch len(seg) % 4 {
	case 2:
		seg += "=="
	case 3:
		seg += "="
	}
	data, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}
