package transport

import (
	"net/http"
)

// cookieWrap is an http.RoundTripper that attaches cookies from a Jar
// before delegating to the inner RoundTripper, and stores response cookies
// back into the Jar. This enables session persistence even when callers
// use the RoundTripper directly instead of an http.Client.
type cookieWrap struct {
	inner http.RoundTripper
	jar   http.CookieJar
}

// WrapWithCookieJar wraps the provided RoundTripper so that cookies from the
// provided jar are sent and updated on each request/response.
func WrapWithCookieJar(inner http.RoundTripper, jar http.CookieJar) http.RoundTripper {
	if jar == nil || inner == nil {
		return inner
	}
	return &cookieWrap{inner: inner, jar: jar}
}

func (w *cookieWrap) RoundTrip(req *http.Request) (*http.Response, error) {
	// clone req to avoid mutating caller headers
	clone := req.Clone(req.Context())
	// add cookies from jar
	if w.jar != nil {
		for _, c := range w.jar.Cookies(clone.URL) {
			clone.AddCookie(c)
		}
	}
	resp, err := w.inner.RoundTrip(clone)
	if err != nil {
		return nil, err
	}
	if w.jar != nil {
		w.jar.SetCookies(clone.URL, resp.Cookies())
	}
	return resp, nil
}
