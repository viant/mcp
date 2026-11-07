package manager

import (
	"errors"
	authcfg "github.com/viant/mcp/client/auth/config"
)

type LinkRequiredError = authcfg.OAuthLinkRequiredError

func LinkRequired(err error) (*LinkRequiredError, bool) {
	var result *LinkRequiredError
	ok := errors.As(err, &result)
	return result, ok
}
