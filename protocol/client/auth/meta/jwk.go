package meta

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
)

// JSONWebKey represents one JSON Web Key.
//
// Supported key types (kty):
//   - "RSA"  – modulus N, exponent E
//   - "EC"   – curve crv, coordinates x & y
//   - "oct"  – symmetric key material k
//
// All common metadata fields are included; unknown members round-trip via Extra.
type JSONWebKey struct {
	// REQUIRED
	Kty string `json:"kty"` // Key Type (RSA, EC, oct, OKP …)

	// Public-key use or permitted operations
	Use    string   `json:"use,omitempty"`     // "sig"|"enc"
	KeyOps []string `json:"key_ops,omitempty"` // ["sign","verify", …]

	Alg string `json:"alg,omitempty"` // Algorithm (e.g. "RS256")
	Kid string `json:"kid,omitempty"` // Key ID (hint for key selection)

	// ----- RSA fields (kty == "RSA") -----
	N string `json:"n,omitempty"` // Modulus   (base64url-encoded)
	E string `json:"e,omitempty"` // Exponent  (base64url-encoded)

	// ----- EC fields (kty == "EC") -----
	Crv string `json:"crv,omitempty"` // Curve  ("P-256", "secp256k1", …)
	X   string `json:"x,omitempty"`   // X coordinate (base64url)
	Y   string `json:"y,omitempty"`   // Y coordinate (base64url)

	// ----- Symmetric / octet fields (kty == "oct") -----
	K string `json:"k,omitempty"` // Key material (base64url)

	// ----- X.509 certificate chain / thumbprints -----
	X5u     string   `json:"x5u,omitempty"`      // URL for cert set
	X5c     []string `json:"x5c,omitempty"`      // PEM-encoded cert chain
	X5t     string   `json:"x5t,omitempty"`      // SHA-1 thumbprint
	X5tS256 string   `json:"x5t#S256,omitempty"` // SHA-256 thumbprint

	// Catch-all for any future / private parameters
	Extra map[string]any `json:"-"`
}

// JSONWebKeySet represents a set of JSON Web Keys.
type JSONWebKeySet struct {
	Keys []JSONWebKey `json:"keys"`
}

// FetchJSONWebKeySet downloads a JWKS and returns a map kid → crypto.PublicKey.  Supports RSA, EC (P-256 / P-384 / P-521) and OKP (Ed25519) keys.
func FetchJSONWebKeySet(ctx context.Context, jwksURL string, client *http.Client) (map[string]crypto.PublicKey, error) {

	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]crypto.PublicKey, len(jwks.Keys))

	for _, k := range jwks.Keys {
		switch k.Kty {

		case "RSA":
			pub, err := parseRSAPublicKey(k.N, k.E)
			if err != nil {
				return nil, fmt.Errorf("kid=%s: %w", k.Kid, err)
			}
			keys[k.Kid] = pub

		case "EC":
			curve, err := curveForName(k.Crv)
			if err != nil {
				return nil, fmt.Errorf("kid=%s: %w", k.Kid, err)
			}
			xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
			if err != nil {
				return nil, fmt.Errorf("kid=%s: decode x: %w", k.Kid, err)
			}
			yBytes, err := base64.RawURLEncoding.DecodeString(k.Y)
			if err != nil {
				return nil, fmt.Errorf("kid=%s: decode y: %w", k.Kid, err)
			}
			pub := &ecdsa.PublicKey{
				Curve: curve,
				X:     new(big.Int).SetBytes(xBytes),
				Y:     new(big.Int).SetBytes(yBytes),
			}
			keys[k.Kid] = pub

		case "OKP": // RFC 8037 (Ed25519 / Ed448, X25519 / X448 for DH)
			if k.Crv != "Ed25519" {
				return nil, fmt.Errorf("kid=%s: unsupported OKP curve %q", k.Kid, k.Crv)
			}
			xBytes, err := base64.RawURLEncoding.DecodeString(k.X)
			if err != nil {
				return nil, fmt.Errorf("kid=%s: decode x: %w", k.Kid, err)
			}
			if l := len(xBytes); l != ed25519.PublicKeySize {
				return nil, fmt.Errorf("kid=%s: Ed25519 key length %d != %d", k.Kid, l, ed25519.PublicKeySize)
			}
			keys[k.Kid] = ed25519.PublicKey(xBytes)

		default:
			// silently ignore unsupported kty values or return error if preferred
		}
	}
	return keys, nil
}

// parseRSAPublicKey creates an RSA public key from modulus and exponent
func parseRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode e: %w", err)
	}

	eInt := int(new(big.Int).SetBytes(eBytes).Uint64())
	n := new(big.Int).SetBytes(nBytes)

	return &rsa.PublicKey{
		N: n,
		E: eInt,
	}, nil
}

func curveForName(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256", "prime256v1":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported EC curve %q", name)
	}
}
