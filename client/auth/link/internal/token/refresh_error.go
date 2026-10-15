package token

import "errors"

// RefreshInvalidGrantError classifies a parsed invalid_grant response from the
// OAuth token endpoint without coupling the token manager to service/auth.
type RefreshInvalidGrantError struct {
	cause error
}

// NewRefreshInvalidGrantError wraps a parsed invalid_grant response.
func NewRefreshInvalidGrantError(cause error) error {
	return &RefreshInvalidGrantError{cause: cause}
}

func (e *RefreshInvalidGrantError) Error() string {
	return "oauth refresh invalid_grant"
}

func (e *RefreshInvalidGrantError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// IsRefreshInvalidGrant reports whether err carries invalid_grant
// classification from the OAuth refresh broker.
func IsRefreshInvalidGrant(err error) bool {
	var target *RefreshInvalidGrantError
	return errors.As(err, &target)
}
