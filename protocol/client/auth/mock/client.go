package mock

import (
	"github.com/viant/afs/url"
	"github.com/viant/mcp/protocol/client/auth/client"
	"golang.org/x/oauth2"
)

func NewTestClient(issuer string) *client.Config {
	return client.NewConfig("test_client_id", "test_client_secret", oauth2.Endpoint{
		AuthURL:   url.Join(issuer, "authorize"),
		TokenURL:  url.Join(issuer, "token"),
		AuthStyle: oauth2.AuthStyleInHeader,
	}, "openid")
}
