package auth

import (
	"net/http"
	"strings"
)

// extractProtoAndHost extracts the outer scheme/host the client saw.
// It understands both the RFC 7239 Forwarded header and the older
// X-Forwarded-Proto / X-Forwarded-Host headers.
func extractProtoAndHost(r *http.Request) (proto, host string) {
	// RFC 7239 Forwarded: proto=https;host=example.com
	if fwd := r.Header.Get("Forwarded"); fwd != "" {
		for _, part := range strings.Split(fwd, ";") {
			pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(pair) != 2 {
				continue
			}
			switch strings.ToLower(pair[0]) {
			case "proto":
				proto = strings.ToLower(pair[1])
			case "host":
				host = pair[1]
			}
		}
	}

	// De-facto standard used by most LBs/CDNs.
	if proto == "" {
		proto = strings.ToLower(r.Header.Get("X-Forwarded-Proto"))
	}
	if host == "" {
		host = r.Header.Get("X-Forwarded-Host")
	}
	// Many LBs put the host in X-Forwarded-Host or in a plain Host header
	// inside X-Forwarded-*; we only take the first element in case there
	// are multiple.
	if idx := strings.IndexByte(host, ','); idx > 0 {
		host = host[:idx]
	}
	if idx := strings.IndexByte(proto, ','); idx > 0 {
		proto = proto[:idx]
	}
	// Final fallback if LB didn’t supply headers.
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	if host == "" {
		host = r.Host
	}

	return proto, host
}
