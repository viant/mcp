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
	Type            string       `yaml:"type" json:"type"  short:"T" long:"transport-type" description:"mcp transport type, e.g., stdio, sse, streamable" choice:"stdio" choice:"sse" choice:"streamable"`
	Port            int          `yaml:"port" json:"port"`
	Endpoint        string       `yaml:"endpoint" json:"endpoint"`
	MessageEndpoint string       `yaml:"messageEndpoint" json:"messageEndpoint"`
	Cors            *server.Cors `yaml:"cors" json:"cors"`
	// Optional HTTP transport configuration
	SSEURI        string `yaml:"sseURI" json:"sseURI"`
	SSEMessageURI string `yaml:"sseMessageURI" json:"sseMessageURI"`
	StreamableURI string `yaml:"streamableURI" json:"streamableURI"`
	RootRedirect  bool   `yaml:"rootRedirect" json:"rootRedirect"`
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
func NewServer(newHandler protoserver.NewHandler, options *ServerOptions) (*server.Server, error) {
	if newHandler == nil {
		return nil, fmt.Errorf("new implementer was nil")
	}

	// Start with mandatory implementer option
	var serverOptions []server.Option
	serverOptions = append(serverOptions, server.WithNewHandler(newHandler))

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
			switch transportOptions.Type {
			case "streamable":
				useStreaming = true
			case "sse":
				useStreaming = false
			}

			// nested transport options
			if transportOptions.Options != nil {
				if transportOptions.Options.Port > 0 {
					serverOptions = append(serverOptions, server.WithEndpointAddress(fmt.Sprintf(":%v", transportOptions.Options.Port)))
				}
				// streamable detection can be provided here as well
				switch transportOptions.Options.Type {
				case "streamable":
					useStreaming = true
				case "sse":
					useStreaming = false
				}

				// CORS configuration
				if transportOptions.Options.Cors != nil {
					serverOptions = append(serverOptions, server.WithCORS(transportOptions.Options.Cors))
				}

				// HTTP transport URIs and root redirect
				if transportOptions.Options.SSEURI != "" {
					serverOptions = append(serverOptions, server.WithSSEURI(transportOptions.Options.SSEURI))
				}
				if transportOptions.Options.SSEMessageURI != "" {
					serverOptions = append(serverOptions, server.WithSSEMessageURI(transportOptions.Options.SSEMessageURI))
				}
				if transportOptions.Options.StreamableURI != "" {
					serverOptions = append(serverOptions, server.WithStreamableURI(transportOptions.Options.StreamableURI))
				}
				if transportOptions.Options.RootRedirect {
					serverOptions = append(serverOptions, server.WithRootRedirect(true))
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
					serverOptions = append(serverOptions, server.WithCustomHTTPHandler(path, handler))
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
		srv.UseStreamableHTTP(true)
	}

	return srv, nil
}
