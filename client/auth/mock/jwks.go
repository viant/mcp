package mock

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/viant/mcp-protocol/oauth2/meta"
	"math/big"
	"net/http"
)

// defaultJwksHandler handles /jwks requests by exposing the server's public key
func (m *AuthorizationService) defaultJwksHandler(w http.ResponseWriter, _ *http.Request) {
	pubKey := m.PrivateKey.Public().(*rsa.PublicKey)
	nBytes := pubKey.N.Bytes()
	eBytes := new(big.Int).SetInt64(int64(pubKey.E)).Bytes()
	nB64 := base64.RawURLEncoding.EncodeToString(nBytes)
	eB64 := base64.RawURLEncoding.EncodeToString(eBytes)
	kidBytes := make([]byte, 8)
	_, _ = rand.Read(kidBytes)
	kid := base64.RawURLEncoding.EncodeToString(kidBytes)
	jwk := meta.JSONWebKey{Kty: "RSA", Use: "sig", Alg: "RS256", Kid: kid, N: nB64, E: eB64}
	jwks := meta.JSONWebKeySet{Keys: []meta.JSONWebKey{jwk}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jwks)
}
