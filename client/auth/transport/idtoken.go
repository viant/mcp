package transport

import (
	"context"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	meta "github.com/viant/mcp-protocol/oauth2/meta"
	"golang.org/x/oauth2"
	"math/rand"
	"net/http"
	"time"
)

func (r *RoundTripper) IdToken(ctx context.Context, token *oauth2.Token, resourceMetadata *meta.ProtectedResourceMetadata) (*oauth2.Token, error) {
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	authServers := resourceMetadata.AuthorizationServers
	issuer := authServers[rnd.Intn(len(authServers))]
	metadata, _ := r.store.LookupAuthorizationServerMetadata(issuer)
	if metadata == nil {
		return nil, fmt.Errorf("authorization server metadata not found for %v", issuer)
	}
	var idTokenString string
	if value := token.Extra("id_token"); value != nil {
		idTokenString, _ = value.(string)
	}
	if idTokenString == "" {
		return nil, errors.New("failed to get identity token")
	}
	keys, ok := r.store.LookupIssuerPublicKeys(metadata.Issuer)
	var err error
	if !ok {
		keys, err = meta.FetchJSONWebKeySet(ctx, metadata.JSONWebKeySetURI, &http.Client{Transport: r.transport})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch JSON Web Key Set: %w", err)
		}
		if err := r.store.AddIssuerPublicKeys(metadata.Issuer, keys); err != nil {
			return nil, fmt.Errorf("failed to store issuer public keys: %w", err)
		}
	}

	idToken, err := jwt.Parse(idTokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); ok {
			kid := token.Header["kid"]
			if kid == nil {
				return nil, fmt.Errorf("kid header not found")
			}
			key, ok := keys[kid.(string)]
			if !ok {
				return nil, fmt.Errorf("key %v not found", kid)
			}
			return key, nil
		}
		return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
	})
	if err != nil {
		return nil, fmt.Errorf("failed to parse id token: %w", err)
	}
	if !idToken.Valid {
		return nil, fmt.Errorf("id token is not valid")
	}
	expiryTime, err := idToken.Claims.GetExpirationTime()
	if err != nil {
		return nil, fmt.Errorf("failed to get expiration time: %w", err)
	}
	return &oauth2.Token{
		TokenType:    "Bearer",
		AccessToken:  idTokenString,
		RefreshToken: token.RefreshToken,
		Expiry:       expiryTime.Add(0),
	}, nil
}
