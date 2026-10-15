// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

import "strings"

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
