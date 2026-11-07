package catalog

import "errors"

// ErrResourceDenied identifies producer rejection of a resource revision.
var ErrResourceDenied = errors.New("resource revision denied")
