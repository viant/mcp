package store

import (
	"golang.org/x/oauth2"
	"testing"
)

func TestMemoryCredentialSnapshotsAndOpaqueExtraWitness(t *testing.T) {
	s := NewMemoryStore()
	witness := s.(interface{ CredentialScope() (string, bool) })
	extras := map[string]any{"id_token": "original-id", "custom": "preserved"}
	token := (&oauth2.Token{AccessToken: "original"}).WithExtra(extras)
	key := TokenKey{Issuer: "fixture", Scopes: "metadata"}
	if err := s.AddToken(key, token); err != nil {
		t.Fatal(err)
	}
	config := &oauth2.Config{ClientID: "original-client", Scopes: []string{"read"}}
	if err := s.AddClientConfig("fixture", config); err != nil {
		t.Fatal(err)
	}
	before, ok := witness.CredentialScope()
	if !ok {
		t.Fatal("missing witness")
	}
	token.AccessToken = "caller mutation"
	config.ClientID = "caller mutation"
	config.Scopes[0] = "caller mutation"
	loaded, _ := s.LookupToken(key)
	client, _ := s.LookupClientConfig("fixture")
	if loaded.AccessToken != "original" || client.ClientID != "original-client" || client.Scopes[0] != "read" {
		t.Fatal("caller changed stored credential")
	}
	loaded.AccessToken = "lookup mutation"
	client.ClientID = "lookup mutation"
	client.Scopes[0] = "lookup mutation"
	after, ok := witness.CredentialScope()
	if !ok || before != after {
		t.Fatal("detached lookup changed credential witness")
	}
	original, _ := s.LookupToken(key)
	if original.Extra("custom") != "preserved" {
		t.Fatal("unknown token extra lost")
	}
	extras["id_token"] = "changed-id"
	changed, ok := witness.CredentialScope()
	if !ok || changed == after {
		t.Fatal("opaque credential extra mutation stayed invisible")
	}
	extras["id_token"] = map[string]any{"unsupported": true}
	if _, ok := witness.CredentialScope(); ok {
		t.Fatal("unsupported credential shape declared cache-safe")
	}
}
