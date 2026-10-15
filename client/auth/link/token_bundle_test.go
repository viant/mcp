package link

import (
	"context"
	scyauth "github.com/viant/scy/auth"
	"golang.org/x/oauth2"
	"testing"
)

func TestWithTokensPreservesIngressWhileInjectTokensSetsIt(t *testing.T) {
	parent := WithIDToken(WithBearer(context.Background(), "ingress-access"), "ingress-id")
	token := &scyauth.Token{Token: oauth2.Token{AccessToken: "bundle-access"}, IDToken: "bundle-id"}
	child := WithTokens(parent, token)
	if Bearer(child) != "ingress-access" || IDToken(child) != "ingress-id" {
		t.Fatal("bundle storage overwrote ingress")
	}
	if MCPAuthToken(child, false) != "bundle-access" || MCPAuthToken(child, true) != "bundle-id" {
		t.Fatal("bundle did not reach outbound selection")
	}
	injected := InjectTokens(parent, token)
	if Bearer(injected) != "bundle-access" || IDToken(injected) != "bundle-id" {
		t.Fatal("explicit ingress injection changed behavior")
	}
	if Bearer(parent) != "ingress-access" || IDToken(parent) != "ingress-id" {
		t.Fatal("parent mutated")
	}
}
