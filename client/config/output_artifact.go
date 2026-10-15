// SPDX-License-Identifier: Apache-2.0
// MCP client configuration extracted from Viant Agently Core.
package config

import (
	"fmt"
	"mime"
	"strings"
	"unicode"
)

// OutputArtifact is an explicit client policy for binary MCP output capture.
// Name and MimeType provide defaults when optional metadata paths are missing
// or empty. BytesPath requires base64 encoding and never falls back to model
// text. Paths are JSON Pointers into raw MCP content/structuredContent; remote
// URIs are never interpreted as paths or public provenance.
type OutputArtifact struct {
	// Role is trusted workspace policy, never inferred from a MIME type or name.
	Role         string `json:"role,omitempty" yaml:"role,omitempty"`
	Name         string `json:"name,omitempty" yaml:"name,omitempty"`
	NamePath     string `json:"namePath,omitempty" yaml:"namePath,omitempty"`
	BytesPath    string `json:"bytesPath,omitempty" yaml:"bytesPath,omitempty"`
	MimeTypePath string `json:"mimeTypePath,omitempty" yaml:"mimeTypePath,omitempty"`
	MimeType     string `json:"mimeType,omitempty" yaml:"mimeType,omitempty"`
	Encoding     string `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

func (p OutputArtifact) Validate() error {
	if p.Role != "" && p.Role != "screenshot" {
		return fmt.Errorf("invalid output artifact role")
	}
	if p.MimeType != "" {
		if _, _, err := mime.ParseMediaType(p.MimeType); err != nil || len(p.MimeType) > 256 {
			return fmt.Errorf("invalid output artifact MIME type")
		}
	}
	if len(p.Name) > 255 || p.Name == "." || p.Name == ".." || strings.ContainsAny(p.Name, `/\\`) || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 {
		return fmt.Errorf("invalid output artifact filename")
	}
	if (p.BytesPath != "" && p.Encoding != "base64") || (p.BytesPath == "" && p.Encoding != "") {
		return fmt.Errorf("output artifact bytes path requires explicit base64 encoding")
	}
	for _, path := range []string{p.NamePath, p.BytesPath, p.MimeTypePath} {
		if path != "" {
			if _, err := OutputArtifactPointer(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// OutputArtifactPointer accepts only bounded RFC 6901 pointers into the raw
// MCP result's content or structuredContent, with no expression syntax.
func OutputArtifactPointer(path string) ([]string, error) {
	invalid := func() ([]string, error) { return nil, fmt.Errorf("invalid output artifact JSON pointer") }
	if len(path) > 1024 || !strings.HasPrefix(path, "/") || strings.Contains(path, "${") || strings.Contains(path, "*") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return invalid()
	}
	tokens := strings.Split(path[1:], "/")
	if len(tokens) < 2 || len(tokens) > 32 || (tokens[0] != "structuredContent" && tokens[0] != "content") {
		return invalid()
	}
	for i, token := range tokens {
		var decoded strings.Builder
		for j := 0; j < len(token); j++ {
			if token[j] != '~' {
				decoded.WriteByte(token[j])
				continue
			}
			j++
			if j >= len(token) || (token[j] != '0' && token[j] != '1') {
				return invalid()
			}
			if token[j] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
		}
		tokens[i] = decoded.String()
	}
	return tokens, nil
}
