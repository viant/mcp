package mock

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// createJWT creates a signed JWT token for clientID with the given type and expiry
func (m *AuthorizationService) createJWT(clientID, tokenType string, expiry time.Duration) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": m.Issuer,
		"sub": "test_subject",
		"aud": clientID,
		"exp": now.Add(expiry).Unix(),
		"iat": now.Unix(),
		"typ": tokenType,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(m.PrivateKey)
}
