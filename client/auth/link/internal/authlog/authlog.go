package authlog

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const Prefix = "[auth-token]"
const maxSafeRunes = 512

var (
	authSchemePattern  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[^\s,;]+`)
	sensitiveKVPattern = regexp.MustCompile(`(?i)(\b(?:access_token|refresh_token|id_token|client_secret|authorization|proxy-authorization|cookie|set-cookie|enc_token|password|passwd|secret|token|dsn)\b["']?\s*(?:=|:)\s*)(?:"[^"]*"|'[^']*'|[^\s&,;]+)`)
	dsnPattern         = regexp.MustCompile(`(?i)(?:\b(?:postgres(?:ql)?|mysql|sqlserver)://[^\s"'<>]+|\b[^\s:/]+:[^@\s]+@(?:tcp|unix)\([^)]*\)/[^\s]*)`)
	urlPattern         = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
)

// Event is a safe, stable token-lifecycle log record. Empty fields remain
// present in the log so automated searches can rely on a consistent shape.
type Event struct {
	Op             string
	UserID         string
	Provider       string
	Caller         string
	ScheduleID     string
	RunID          string
	Endpoint       string
	HTTPStatus     int
	OAuthErrorCode string
	Classification string
	Action         string
	Elapsed        time.Duration
	Recovered      bool
	Err            error
}

// Log writes an always-on token lifecycle event with safe structured fields.
func Log(ctx context.Context, event Event) {
	caller, scheduleID, runID := contextFields(ctx)
	if strings.TrimSpace(event.Caller) == "" {
		event.Caller = caller
	}
	if strings.TrimSpace(event.ScheduleID) == "" {
		event.ScheduleID = scheduleID
	}
	if strings.TrimSpace(event.RunID) == "" {
		event.RunID = runID
	}
	host, path := Endpoint(event.Endpoint)
	log.Printf(Prefix+` op=%q user_id=%q provider=%q caller=%q schedule_id=%q run_id=%q endpoint_host=%q endpoint_path=%q http_status=%d oauth_error=%q classification=%q action=%q recovered=%t elapsed_ms=%d err=%q`,
		Sanitize(event.Op),
		SafeUserID(event.UserID),
		Sanitize(event.Provider),
		Sanitize(event.Caller),
		Sanitize(event.ScheduleID),
		Sanitize(event.RunID),
		Sanitize(host),
		Sanitize(path),
		event.HTTPStatus,
		Sanitize(event.OAuthErrorCode),
		Sanitize(event.Classification),
		Sanitize(event.Action),
		event.Recovered,
		event.Elapsed.Milliseconds(),
		Sanitize(errorString(event.Err)),
	)
}

// Endpoint returns only the host and path of a URL. Userinfo, query, and
// fragments are deliberately discarded.
func Endpoint(raw string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", ""
	}
	return parsed.Hostname(), cleanPath(parsed.EscapedPath())
}

// Sanitize redacts common credential representations and URL secrets before
// truncating to a bounded number of Unicode characters.
func Sanitize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = dsnPattern.ReplaceAllString(value, "[REDACTED_DSN]")
	value = urlPattern.ReplaceAllStringFunc(value, sanitizeURL)
	value = authSchemePattern.ReplaceAllString(value, "$1 [REDACTED]")
	value = sensitiveKVPattern.ReplaceAllString(value, "$1[REDACTED]")
	runes := []rune(value)
	if len(runes) > maxSafeRunes {
		value = string(runes[:maxSafeRunes])
	}
	return strings.ToValidUTF8(value, "\uFFFD")
}

// Host scheduling metadata is supplied explicitly in Event fields.
func contextFields(context.Context) (string, string, string) { return "interactive", "", "" }

func sanitizeURL(raw string) string {
	suffix := ""
	for len(raw) > 0 {
		last, size := utf8.DecodeLastRuneInString(raw)
		if !strings.ContainsRune(".,;:)]}", last) {
			break
		}
		suffix = string(last) + suffix
		raw = raw[:len(raw)-size]
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "[REDACTED_URL]" + suffix
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.Scheme + "://" + parsed.Host + cleanPath(parsed.EscapedPath()) + suffix
}

func cleanPath(path string) string {
	if path == "" {
		return ""
	}
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	return path
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}

// SafeUserID returns a sanitized canonical identifier and suppresses values
// that look like credential or configuration URLs.
func SafeUserID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "://") || strings.ContainsAny(value, "?#") {
		return ""
	}
	return Sanitize(value)
}

// Status is useful for logging a status when the caller only has a string.
func Status(value string) int {
	status, _ := strconv.Atoi(strings.TrimSpace(value))
	return status
}
