package mock

import (
	"fmt"
	"net/http"
)

// defaultAuthorizeHandler handles /authorize requests
func (m *AuthorizationService) defaultAuthorizeHandler(w http.ResponseWriter, r *http.Request) {
	//if r.Method != http.MethodGet {
	//	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	//	return
	//}
	clientID := r.URL.Query().Get("client_id")
	if clientID != m.ClientID {
		http.Error(w, "Invalid client ID", http.StatusBadRequest)
		return
	}
	redirectURI := r.URL.Query().Get("redirect_uri")
	if redirectURI == "" {
		http.Error(w, "Missing redirect URI", http.StatusBadRequest)
		return
	}
	state := r.URL.Query().Get("state")
	code := "test_authorization_code"
	redirectURL := fmt.Sprintf("%s?code=%s&state=%s", redirectURI, code, state)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}
