package endpoint

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// Server represents an auth callback endpoint
type Server struct {
	// Port is the port the server is listening on
	Port int
	// err stores any error that occurred during server startup or operation
	err error
	// listener is the TCP listener for the server
	listener net.Listener
	// server is the HTTP server instance
	server *http.Server
	// httpHandler is the handler for HTTP requests
	*httpHandler
}

// Close closes the endpoint
func (s *Server) Close() {
	select {
	case s.httpHandler.done <- true:
		// Signal already sent, nothing to do
	default:
		// No signal sent yet, send it
	}
}

// Wait waits until the server receives the authentication callback
func (s *Server) Wait() error {
	select {
	case <-s.httpHandler.done:
		// Grace period to allow response to be sent before shutting down
		go func() {
			time.Sleep(1 * time.Second)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.server.Shutdown(ctx); err != nil {
				log.Printf("Error shutting down server: %v", err)
			}
		}()
	case <-time.After(5 * time.Minute):
		// Timeout after 5 minutes
		s.Close()
		return errors.New("authentication timed out")
	}
	return s.err
}

// Start starts the endpoint and listens for requests
func (s *Server) Start() {
	err := s.server.Serve(s.listener)
	if err != nil {
		if !strings.Contains(err.Error(), "closed") {
			s.err = err
			select {
			case s.httpHandler.done <- true:
				// Signal sent
			default:
				// Signal already sent, nothing to do
			}
			return
		}
		s.err = nil
	}
}

// New creates an auth callback endpoint on a random available port
func New() (*Server, error) {
	result := &Server{httpHandler: newHttpHandler()}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP endpoint: %w", err)
	}
	result.listener = listener
	result.Port = listener.Addr().(*net.TCPAddr).Port

	// Configure the HTTP server with reasonable timeout settings
	result.server = &http.Server{
		Addr:         fmt.Sprintf(":%v", result.Port),
		Handler:      result.httpHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	log.Printf("OAuth callback server started on port %d", result.Port)
	return result, nil
}
