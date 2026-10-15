// SPDX-License-Identifier: Apache-2.0
// MCP client configuration extracted from Viant Agently Core.
package config

// SkillDiscovery enables on-demand federation. No remote documents are loaded
// during runtime construction or local skill watching.
type SkillDiscovery struct {
	Tools   *SkillToolBridge `yaml:"tools,omitempty" json:"tools,omitempty"`
	Enabled bool             `yaml:"enabled" json:"enabled"`
	// CatalogID is required when the configured server name is not URI-safe.
	CatalogID        string `yaml:"catalogId,omitempty" json:"catalogId,omitempty"`
	MaxPages         int    `yaml:"maxPages,omitempty" json:"maxPages,omitempty"`
	MaxScannedItems  int    `yaml:"maxScannedItems,omitempty" json:"maxScannedItems,omitempty"`
	MaxSkills        int    `yaml:"maxSkills,omitempty" json:"maxSkills,omitempty"`
	MaxMetadataBytes int    `yaml:"maxMetadataBytes,omitempty" json:"maxMetadataBytes,omitempty"`
	MaxSkillBytes    int    `yaml:"maxSkillBytes,omitempty" json:"maxSkillBytes,omitempty"`
	TimeoutSec       int    `yaml:"timeoutSec,omitempty" json:"timeoutSec,omitempty"`
}

// SkillToolBridge explicitly maps legacy tools to skill metadata operations.
// Results must contain SEP-2640 entries; this does not advertise an extension.
type SkillToolBridge struct {
	List string `yaml:"list" json:"list"`
	Get  string `yaml:"get" json:"get"`
}
