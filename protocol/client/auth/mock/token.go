package mock

import (
	"encoding/json"
	"net/http"
	"time"
)

// defaultTokenHandler handles /token requests
func (m *AuthorizationService) defaultTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}
	grantType := r.FormValue("grant_type")
	if grantType != "authorization_code" && grantType != "refresh_token" {
		http.Error(w, "Unsupported grant type", http.StatusBadRequest)
		return
	}
	clientID, clientSecret, ok := r.BasicAuth()
	if !ok {
		clientID = r.FormValue("client_id")
		clientSecret = r.FormValue("client_secret")
	}
	if clientID != m.ClientID || clientSecret != m.ClientSecret {
		http.Error(w, "Invalid client credentials", http.StatusUnauthorized)
		return
	}
	expiresIn := 3600
	accessToken, err := m.createJWT(clientID, "access_token", time.Duration(expiresIn)*time.Second)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	refreshToken, err := m.createJWT(clientID, "refresh_token", 24*time.Hour)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	idToken, err := m.createJWT(clientID, "id_token", time.Duration(expiresIn)*time.Second)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	response := map[string]interface{}{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"refresh_token": refreshToken,
		"expires_in":    expiresIn,
		"id_token":      idToken,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
