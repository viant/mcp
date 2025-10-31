package namespace

import "errors"

var (
	// ErrMisconfigured is returned when required fields are not set on NamespaceService.
	ErrMisconfigured = errors.New("namespace: misconfigured resolver (missing Provider or Factory)")
)
