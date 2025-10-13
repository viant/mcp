package mcp

type Options struct {
	URL                      string `short:"u" long:"url" description:"mcp url" required:"true"`
	UseIdToken               bool   `short:"i" long:"id-token" description:"use id token"`
	BackendForFrontend       bool   `short:"b" long:"backend-for-frontend" description:"use backend for frontend"`
	BackendForFrontendHeader string `short:"h" long:"backend-for-frontend-header" description:"backend for frontend header"`
	OAuth2ConfigURL          string `short:"c" long:"config" description:"oauth2 config file"`
	EncryptionKey            string `short:"k" long:"key" description:"encryption key"`

	// Built-in web elicitator to handle elicitation when downstream client lacks support.
	ElicitatorEnabled     bool   `long:"elicit" description:"enable built-in web elicitator when client lacks elicitation"`
	ElicitatorListenAddr  string `long:"elicit-listen" description:"elicitator listen address" default:"127.0.0.1:0"`
	ElicitatorOpenBrowser bool   `long:"elicit-open" description:"attempt to open browser for elicitation"`
}
