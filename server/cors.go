package server

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	AllowOriginHeader       = "Access-Control-Allow-Origin"
	AllowHeadersHeader      = "Access-Control-Allow-Headers"
	AllowMethodsHeader      = "Access-Control-Allow-Methods"
	AllControlRequestHeader = "Access-Control-Request-Method"
	AllowCredentialsHeader  = "Access-Control-Allow-Credentials"
	ExposeHeadersHeader     = "Access-Control-Expose-Headers"
	MaxAgeHeader            = "Access-Control-Max-Age"
	Separator               = ", "
)

type Cors struct {
	AllowCredentials *bool    `yaml:"AllowCredentials,omitempty"`
	AllowHeaders     []string `yaml:"AllowHeaders,omitempty"`
	AllowMethods     []string `yaml:"AllowMethods,omitempty"`
	AllowOrigins     []string `yaml:"AllowOrigins,omitempty"`
	ExposeHeaders    []string `yaml:"ExposeHeaders,omitempty"`
	MaxAge           *int64   `yaml:"MaxAge,omitempty"`
}

func (c *Cors) OriginMap() map[string]bool {
	var result = make(map[string]bool)
	for _, origin := range c.AllowOrigins {
		result[origin] = true
	}
	return result
}

// corsHandler is a handler that sets CORS headers
type corsHandler struct {
	*Cors
}

func (h *corsHandler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Cors.setHeaders(w, r)
		next.ServeHTTP(w, r)
	})
}

func (c *Cors) setHeaders(writer http.ResponseWriter, request *http.Request) {
	if c == nil {
		return
	}
	origin := request.Header.Get("Origin")
	allowedOrigins := c.OriginMap()
	if allowedOrigins["*"] {
		if origin == "" {
			writer.Header().Set(AllowOriginHeader, "*")
		} else {
			writer.Header().Set(AllowOriginHeader, origin)
		}
	} else {
		if origin != "" && allowedOrigins[origin] {
			writer.Header().Set(AllowOriginHeader, origin)
		}
	}
	if c.AllowMethods != nil {
		writer.Header().Set(AllowMethodsHeader, request.Method)
	}
	if request.Method == "OPTIONS" {
		requestMethod := request.Header.Get(AllControlRequestHeader)
		if requestMethod != "" {
			writer.Header().Set(AllowMethodsHeader, requestMethod)
		}
	}
	if len(c.AllowHeaders) > 0 {
		allowedHeaders := strings.Join(c.AllowHeaders, Separator)
		if allowedHeaders == "*" {
			allowedHeaders = "Content-Type,Authorization,X-MCP-Authorization"
		}
		writer.Header().Set(AllowHeadersHeader, allowedHeaders)
	}
	if c.AllowCredentials != nil {
		writer.Header().Set(AllowCredentialsHeader, strconv.FormatBool(*c.AllowCredentials))
	}
	if c.MaxAge != nil {
		writer.Header().Set(MaxAgeHeader, strconv.Itoa(int(*c.MaxAge)))
	}
	if len(c.ExposeHeaders) > 0 {
		exposedHeaders := strings.Join(c.ExposeHeaders, Separator)
		if exposedHeaders == "*" {
			exposedHeaders = "Content-Type,Authorization"
		}
		writer.Header().Set(ExposeHeadersHeader, exposedHeaders)
	}
}

func defaultCors() *Cors {
	ret := &Cors{
		AllowCredentials: &[]bool{true}[0],
		AllowHeaders:     []string{"*"},
		AllowMethods:     []string{"*"},
		AllowOrigins:     []string{"*"},
		ExposeHeaders:    []string{"*"},
	}
	return ret
}
