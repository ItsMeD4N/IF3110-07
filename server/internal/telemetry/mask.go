package telemetry

import (
	"database/sql/driver"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

const RedactedValue = "[REDACTED]"

var sensitiveKeySubstrings = []string{
	"password",
	"passwd",
	"secret",
	"token",
	"cookie",
	"authorization",
	"csrf",
	"session",
	"credential",
	"encryption_key",
	"api_key",
	"apikey",
	"dsn",
	"private_key",
	"passphrase",
}

var (
	reURLCreds       = regexp.MustCompile(`(?i)\b((?:postgres(?:ql)?|redis|rediss|amqp|amqps|https?)://[^:/@\s]+):([^@\s]+)@`)
	reSQLPassword    = regexp.MustCompile(`(?i)\b(PASSWORD|IDENTIFIED\s+BY|SECRET)\s+'(?:''|[^'])*'`)
	reBearerAuth     = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9\-._~+/]+=*`)
	reCookieSecret   = regexp.MustCompile(`(?i)\b(app_session_id|csrf_token|session_id|session|access_token|refresh_token)=([^;\s,&]+)`)
	reKeyValueSecret = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api_key|encryption_key)\s*[:=]\s*("[^"]*"|'[^']*'|[^\s,;&]+)`)
)

func IsSensitiveKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if lower == "" {
		return false
	}
	if lower == "lock_token" {
		return false
	}
	for _, sub := range sensitiveKeySubstrings {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

func MaskString(s string) string {
	if s == "" {
		return s
	}
	out := reURLCreds.ReplaceAllString(s, "$1:"+RedactedValue+"@")
	out = reSQLPassword.ReplaceAllString(out, "$1 '"+RedactedValue+"'")
	out = reBearerAuth.ReplaceAllString(out, "$1 "+RedactedValue)
	out = reCookieSecret.ReplaceAllString(out, "$1="+RedactedValue)
	out = reKeyValueSecret.ReplaceAllString(out, "$1="+RedactedValue)
	return out
}

func MaskSQL(query string) string {
	masked := MaskString(strings.TrimSpace(query))
	const maxLogQueryLen = 4096
	if len(masked) > maxLogQueryLen {
		return masked[:maxLogQueryLen] + "...[truncated]"
	}
	return masked
}

func MaskParams(params []any) []any {
	if len(params) == 0 {
		return nil
	}
	out := make([]any, len(params))
	for i, p := range params {
		out[i] = maskAny(p)
	}
	return out
}

type Sensitive string

func (s Sensitive) Value() (driver.Value, error) { return string(s), nil }

func maskValuer(v driver.Valuer) (out any) {
	defer func() {
		if recover() != nil {
			out = "[unprintable]"
		}
	}()
	val, err := v.Value()
	if err != nil {
		return "[unprintable]"
	}
	return maskAny(val)
}

func maskAny(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case Sensitive:
		return RedactedValue
	case string:
		if len(x) > 512 {
			return MaskString(x[:512]) + "...[truncated]"
		}
		return MaskString(x)
	case []byte:
		return fmt.Sprintf("[bytes:%d]", len(x))
	case error:
		return MaskString(x.Error())
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			if IsSensitiveKey(k) {
				m[k] = RedactedValue
			} else {
				m[k] = maskAny(val)
			}
		}
		return m
	case map[string]string:
		m := make(map[string]string, len(x))
		for k, val := range x {
			if IsSensitiveKey(k) {
				m[k] = RedactedValue
			} else {
				m[k] = MaskString(val)
			}
		}
		return m
	case []any:
		res := make([]any, len(x))
		for i, item := range x {
			res[i] = maskAny(item)
		}
		return res
	case []string:
		res := make([]string, len(x))
		for i, item := range x {
			res[i] = MaskString(item)
		}
		return res
	case driver.Valuer:
		return maskValuer(x)
	default:
		return x
	}
}

func MaskAttr(a slog.Attr) slog.Attr {
	if a.Equal(slog.Attr{}) {
		return a
	}
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, RedactedValue)
	}
	val := a.Value.Resolve()
	switch val.Kind() {
	case slog.KindGroup:
		attrs := val.Group()
		masked := make([]any, 0, len(attrs))
		for _, sub := range attrs {
			masked = append(masked, MaskAttr(sub))
		}
		return slog.Group(a.Key, masked...)
	case slog.KindString:
		return slog.String(a.Key, MaskString(val.String()))
	case slog.KindAny:
		return slog.Any(a.Key, maskAny(val.Any()))
	default:
		return slog.Attr{Key: a.Key, Value: val}
	}
}
