package endpoint

import (
	_ "embed"
	"log"
	"net/http"
	"net/url"
)

//go:embed asset/success.html
var content string

// httpHandler handles HTTP requests for the OAuth callback
type httpHandler struct {
	// values stores form values from the callback
	values url.Values
	// done is a channel that signals when the callback has been received
	done chan bool
}

// handle processes an individual HTTP request
func (h *httpHandler) handle(writer http.ResponseWriter, request *http.Request) {
	//	log.Printf("Received callback request: %s", request.URL.String())

	if err := request.ParseForm(); err == nil {
		h.values = request.Form

		// Log the received callback data (except sensitive info)
		////log.Printf("Received code: %v", h.values.Get("code") != "")
		//log.Printf("Received state: %v", h.values.Get("state"))

		if h.values.Get("error") != "" {
			log.Printf("Error in callback: %s - %s",
				h.values.Get("error"),
				h.values.Get("error_description"))
		}
	} else {
		log.Printf("Error parsing form: %v", err)
	}

	writer.Header().Set("Content-Type", "text/html")
	writer.WriteHeader(http.StatusOK)
	_, err := writer.Write([]byte(content))
	if err != nil {
		log.Printf("Error writing response: %v", err)
	}

	// Signal that we've received the callback
	select {
	case h.done <- true:
		// Successfully sent signal
	default:
		// Channel already has a value, no need to send again
	}
}

// ServeHTTP handles HTTP requests
func (h *httpHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.handle(writer, request)
}

// AuthCode returns the authorization code from the callback
func (h *httpHandler) AuthCode() string {
	if len(h.values) > 0 {
		return h.values.Get("code")
	}
	return ""
}

// Error returns any error that occurred during the OAuth flow
func (h *httpHandler) Error() string {
	if len(h.values) > 0 {
		return h.values.Get("error")
	}
	return ""
}

// ErrorDescription returns the error description if an error occurred
func (h *httpHandler) ErrorDescription() string {
	if len(h.values) > 0 {
		return h.values.Get("error_description")
	}
	return ""
}

// newHttpHandler creates a new HTTP handler for the OAuth callback
func newHttpHandler() *httpHandler {
	return &httpHandler{
		done: make(chan bool, 1),
	}
}
