package mcp

import (
	"fmt"
	"net/http"

	"github.com/viant/mcp-protocol/authorization"
	"github.com/viant/mcp-protocol/schema"

	protoserver "github.com/viant/mcp-protocol/server"
	"github.com/viant/mcp/server"
	"github.com/viant/mcp/server/auth"
)

// ServerOptions defines options for configuring an MCP server.
type ServerOptions struct {
	Name            string           `yaml:"name" json:"name"`
	Version         string           `yaml:"version" json:"version"`
	ProtocolVersion string           `yaml:"protocol" json:"protocol"  short:"p" long:"protocol" description:"mcp protocol"`
	LoggerName      string           `yaml:"loggerName" json:"loggerName"`
	Transport       *ServerTransport `yaml:"transport" json:"transport"`
}

type ServerTransport struct {
	Type           string                      `yaml:"type" json:"type"`
	Options        *ServerTransportOptions     `yaml:"options" json:"options"`
	Auth           *ServerOptionAuth           `yaml:"-" json:"-"`
	CustomHandlers map[string]http.HandlerFunc `yaml:"-" json:"-"`
}

type ServerTransportOptions struct {
	Type            string       `yaml:"type" json:"type"  short:"T" long:"transport-type" description:"mcp transport type, e.g., stdio, sse, streaming" choice:"stdio" choice:"sse" choice:"streaming"`
	Port            int          `yaml:"port" json:"port"`
	Endpoint        string       `yaml:"endpoint" json:"endpoint"`
	MessageEndpoint string       `yaml:"messageEndpoint" json:"messageEndpoint"`
	Cors            *server.Cors `yaml:"cors" json:"cors"`
}

type ServerOptionAuth struct {
	ProtectedResourcesHandler http.HandlerFunc
	Authorizer                server.Middleware
	JRPCAuthorizer            auth.JRPCAuthorizer //experimental
	UseJRPCAuthorizer         bool                // if true, JRPCAuthorizer will be used for JSON-RPC requests
	//Optional metadata for protected resources
	Policy *authorization.Policy
}

// NewServer creates a new MCP server with the given implementer and options.
func NewServer(newImplementer protoserver.NewImplementer, options *ServerOptions) (*server.Server, error) {
	if newImplementer == nil {
		return nil, fmt.Errorf("new implementer was nil")
	}

	// Start with mandatory implementer option
	var serverOptions []server.Option
	serverOptions = append(serverOptions, server.WithNewImplementer(newImplementer))

	// Flag to switch the HTTP transport to streaming mode
	useStreaming := false

	if options != nil {

		// populate implementation info
		if options.Name != "" || options.Version != "" {
			impl := schema.Implementation{
				Name:    options.Name,
				Version: options.Version,
			}
			serverOptions = append(serverOptions, server.WithImplementation(impl))
		}
		if options.ProtocolVersion != "" {
			// set protocol version if provided
			serverOptions = append(serverOptions, server.WithProtocolVersion(options.ProtocolVersion))
		}

		// logger name override
		if options.LoggerName != "" {
			serverOptions = append(serverOptions, server.WithLoggerName(options.LoggerName))
		}

		if options.Transport != nil {
			transportOptions := options.Transport

			// global transport type detection – top-level declaration wins
			if transportOptions.Type == "streaming" {
				useStreaming = true
			}

			// nested transport options
			if transportOptions.Options != nil {
				if transportOptions.Options.Port > 0 {
					serverOptions = append(serverOptions, server.WithEndpointAddress(fmt.Sprintf(":%v", transportOptions.Options.Port)))
				}
				// streaming detection can be provided here as well
				if transportOptions.Options.Type == "streaming" {
					useStreaming = true
				}

				// CORS configuration
				if transportOptions.Options.Cors != nil {
					serverOptions = append(serverOptions, server.WithCORS(transportOptions.Options.Cors))
				}
			}

			// authentication / authorization plumbing
			if authOptions := transportOptions.Auth; authOptions != nil {
				if policy := authOptions.Policy; policy != nil {
					authService, err := auth.New(&auth.Config{Policy: authOptions.Policy})
					if err != nil {
						return nil, err
					}
					if authOptions.ProtectedResourcesHandler == nil {
						authOptions.ProtectedResourcesHandler = authService.ProtectedResourcesHandler
					}
					if authOptions.Authorizer == nil {
						authOptions.Authorizer = authService.Middleware
					}
					if authOptions.JRPCAuthorizer == nil && authOptions.UseJRPCAuthorizer {
						authOptions.JRPCAuthorizer = authService.EnsureAuthorized
					}
				}

				if authOptions.ProtectedResourcesHandler != nil {
					serverOptions = append(serverOptions, server.WithProtectedResourcesHandler(authOptions.ProtectedResourcesHandler))
				}
				if authOptions.Authorizer != nil {
					serverOptions = append(serverOptions, server.WithAuthorizer(authOptions.Authorizer))
				}
				if authOptions.JRPCAuthorizer != nil {
					serverOptions = append(serverOptions, server.WithJRPCAuthorizer(authOptions.JRPCAuthorizer))
				}
			}
			if len(transportOptions.CustomHandlers) > 0 {
				// add custom handlers to the server options
				for path, handler := range transportOptions.CustomHandlers {
					serverOptions = append(serverOptions, server.WithCustomHandler(path, handler))
				}
			}
		}
	}

	srv, err := server.New(serverOptions...)
	if err != nil {
		return nil, err
	}
	// apply streaming flag
	if useStreaming {
		srv.UseStreaming(true)
	}

	return srv, nil
}
