// SPDX-License-Identifier: Apache-2.0
// Extracted from Viant Agently Core.
package link

// PreferencesPatch describes a partial update to user preferences.
type PreferencesPatch struct {
	DisplayName        *string                           `json:"displayName,omitempty"`
	Timezone           *string                           `json:"timezone,omitempty"`
	DefaultAgentRef    *string                           `json:"defaultAgentRef,omitempty"`
	DefaultModelRef    *string                           `json:"defaultModelRef,omitempty"`
	DefaultEmbedderRef *string                           `json:"defaultEmbedderRef,omitempty"`
	AgentPrefs         map[string]map[string]interface{} `json:"agentPrefs,omitempty"`
}
