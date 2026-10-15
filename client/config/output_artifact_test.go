// SPDX-License-Identifier: Apache-2.0
// MCP client configuration extracted from Viant Agently Core.
package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputArtifactPolicyConfiguration(t *testing.T) {
	var config MCPClient
	require.NoError(t, json.Unmarshal([]byte(`{"outputArtifacts":{"SpreadsheetEditorDownload":{"name":"spreadsheet-editor.xlsx"}}}`), &config))
	policy := config.OutputArtifacts["SpreadsheetEditorDownload"]
	require.NoError(t, policy.Validate())
	require.NoError(t, (OutputArtifact{Name: "screenshot.png", Role: "screenshot"}).Validate())
	require.Error(t, (OutputArtifact{Name: "image.png", Role: "user-image"}).Validate())
	for _, name := range []string{"../workbook.xlsx", "a/b", `a\b`, "..", "a\r\nb", string([]byte{'a', 0})} {
		require.Error(t, (OutputArtifact{Name: name}).Validate())
	}
}

func TestOutputArtifactRestrictedJSONPointers(t *testing.T) {
	for _, path := range []string{"structuredContent/file", "/arguments/data", "/content/*/resource/blob", "/structuredContent/${file}/data", "/structuredContent/a~2b", "/structuredContent", "/content/0/\n"} {
		require.Error(t, (OutputArtifact{BytesPath: path, Encoding: "base64"}).Validate())
	}
	require.Error(t, (OutputArtifact{BytesPath: "/structuredContent/data"}).Validate())
	require.Error(t, (OutputArtifact{Encoding: "base64"}).Validate())
	require.Error(t, (OutputArtifact{MimeType: "bad MIME"}).Validate())
	require.NoError(t, (OutputArtifact{BytesPath: "/content/0/resource/blob", Encoding: "base64", NamePath: "/structuredContent/file/name", MimeType: "application/octet-stream"}).Validate())
}
