package mock

import (
	"github.com/viant/afs/url"
	"golang.org/x/oauth2"
)

func NewTestClient(issuer string) *oauth2.Config {
	return &oauth2.Config{ClientID: "test_client_id", ClientSecret: "test_client_secret", Endpoint: oauth2.Endpoint{
		AuthURL:   url.Join(issuer, "authorize"),
		TokenURL:  url.Join(issuer, "token"),
		AuthStyle: oauth2.AuthStyleInHeader,
	}, Scopes: []string{"openid", "profile", "email"}, RedirectURL: "http://localhost:8080/callback"}
}
