// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

import (
	"encoding/base64"

	"strings"
)

func base64RawURL(b []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(b), "=")
}

func base64RawURLDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}
