package mcp

type Options struct {
	URL                      string `short:"u" long:"url" description:"mcp url" required:"true"`
	UseIdToken               bool   `short:"i" long:"id-token" description:"use id token"`
	BackendForFrontend       bool   `short:"b" long:"backend-for-frontend" description:"use backend for frontend"`
	BackendForFrontendHeader string `short:"h" long:"backend-for-frontend-header" description:"backend for frontend header"`
	OAuth2ConfigURL          string `short:"c" long:"config" description:"oauth2 config file"`
	EncryptionKey            string `short:"k" long:"key" description:"encryption key"`
}
