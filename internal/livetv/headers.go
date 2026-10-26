package livetv

import (
	"net/http"
	"sort"
	"strings"
)

// A provider stream entry may ask the player to send a browser identity before
// it serves media: a referer, an origin, or a player's User-Agent. Those are
// display hints, not credentials, so only this allowlist is forwarded and
// anything else a provider sends (Cookie, Authorization, API keys) is dropped.
var requestHeaderAllowlist = map[string]bool{
	"User-Agent": true,
	"Referer":    true,
	"Origin":     true,
}

const (
	// maxRequestHeaderValue bounds one forwarded value.
	maxRequestHeaderValue = 2048
	// maxRequestHeaderBytes bounds the whole set.
	maxRequestHeaderBytes = 4096
)

// SanitizeRequestHeaders narrows a provider-supplied header set to the
// allowlist. Names are canonicalized and values trimmed; a value that is empty,
// too long or contains a control character is dropped rather than repaired,
// because a newline inside a header value would inject a header or split the
// request. Values are never logged. A nil result means "send nothing extra".
func SanitizeRequestHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	total := 0
	for name, value := range in {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !requestHeaderAllowlist[canonical] {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || len(value) > maxRequestHeaderValue || hasControlChars(value) {
			continue
		}
		if total+len(canonical)+len(value) > maxRequestHeaderBytes {
			continue
		}
		out[canonical] = value
		total += len(canonical) + len(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// requestHeaderBlock renders a sanitized set the way ffmpeg's and ffprobe's
// -headers flag expects: one "Name: value" line per header, CRLF separated and
// sorted so the command is deterministic. User-Agent is left out because both
// tools set it from their own -user_agent option, and sending it in both places
// leaves which one wins up to the build.
func requestHeaderBlock(headers map[string]string) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		if name == "User-Agent" {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(headers[name])
		b.WriteString("\r\n")
	}
	return b.String()
}

func hasControlChars(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
