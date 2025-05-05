package transport

import "net/http"

type Scopper interface {
	Scope(request *http.Request) string
}

type nopScopper struct{}

func (n *nopScopper) Scope(_ *http.Request) string {
	return ""
}
