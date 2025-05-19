package mock

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// defaultResourceHandler simulates a protected resource at /resource
func (m *AuthorizationService) defaultResourceHandler(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(
			`Bearer realm="%s", scope="resource", resource_metadata="%s"`,
			m.Issuer, m.Issuer))
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		http.Error(w, "Invalid authorization header", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(parts[1], "eyJ") {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "This is a protected resource"})
}
