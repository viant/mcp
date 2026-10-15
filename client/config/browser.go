// SPDX-License-Identifier: Apache-2.0
// MCP client configuration extracted from Viant Agently Core.
package config

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// BrowserTransport contains only public transport addressing. Auth, commands,
// cookies, headers and server-side credentials never belong in this descriptor.
type BrowserTransport struct {
	Type             string `json:"type" yaml:"type"`
	ExtensionID      string `json:"extensionId" yaml:"extensionId"`
	PortName         string `json:"portName" yaml:"portName"`
	RequestTimeoutMs int    `json:"requestTimeoutMs,omitempty" yaml:"requestTimeoutMs,omitempty"`
}
type BrowserDescriptor struct {
	Name              string                    `json:"name"`
	ExecutionLocation string                    `json:"executionLocation"`
	Transport         BrowserTransport          `json:"transport"`
	AllowedTools      []string                  `json:"allowedTools"`
	OutputArtifacts   map[string]OutputArtifact `json:"outputArtifacts,omitempty"`
}

var browserName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

func (c *MCPClient) IsBrowserExecution() bool { return c != nil && c.ExecutionLocation == "browser" }
func (c *MCPClient) BrowserDescriptor(name string) (BrowserDescriptor, error) {
	fail := errors.New("invalid browser MCP configuration")
	if c == nil || c.ExecutionLocation != "browser" || !browserName.MatchString(name) || c.BrowserTransport == nil {
		return BrowserDescriptor{}, fail
	}
	t := *c.BrowserTransport
	if t.Type != "chrome-extension" || !regexp.MustCompile(`^[a-p]{32}$`).MatchString(t.ExtensionID) || !regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`).MatchString(t.PortName) || t.RequestTimeoutMs < 0 || t.RequestTimeoutMs > 9000 {
		return BrowserDescriptor{}, fail
	}
	// A browser config is never a fallback for a configured remote/stdio client.
	if c.ClientOptions != nil && (c.ClientOptions.Auth != nil || c.ClientOptions.Transport.URL != "" || c.ClientOptions.Transport.Command != "") {
		return BrowserDescriptor{}, fail
	}
	patterns := append([]string{}, c.BrowserTools...)
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	if len(patterns) > 64 {
		return BrowserDescriptor{}, fail
	}
	for _, p := range patterns {
		if len(p) == 0 || len(p) > 128 || strings.ContainsAny(p, "\\/\x00") {
			return BrowserDescriptor{}, fail
		}
	}
	var artifacts map[string]OutputArtifact
	if len(c.OutputArtifacts) > 0 {
		if len(c.OutputArtifacts) > 64 {
			return BrowserDescriptor{}, fail
		}
		for method, policy := range c.OutputArtifacts {
			if method == "" || len(method) > 256 || strings.ContainsAny(method, "\x00\r\n") || policy.Validate() != nil {
				return BrowserDescriptor{}, fail
			}
		}
		raw, err := json.Marshal(c.OutputArtifacts)
		if err != nil {
			return BrowserDescriptor{}, fail
		}
		if json.Unmarshal(raw, &artifacts) != nil {
			return BrowserDescriptor{}, fail
		}
	}
	return BrowserDescriptor{Name: name, ExecutionLocation: "browser", Transport: t, AllowedTools: patterns, OutputArtifacts: artifacts}, nil
}
func (c *MCPClient) ValidateExecutionLocation() error {
	if c == nil || c.ExecutionLocation == "" || c.ExecutionLocation == "server" {
		if c != nil && (c.BrowserTransport != nil || len(c.BrowserTools) > 0) {
			return errors.New("browser transport requires browser execution")
		}
		return nil
	}
	if c.ExecutionLocation != "browser" {
		return errors.New("unsupported MCP execution location")
	}
	return nil
}
