package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
)

// NormalizeKey converts an arbitrary URL path segment or join code into a
// canonical room key. The same logical room is reached whether a user opens a
// path (e.g. "/r/Team-Cats") or types a code ("team cats"). Disallowed
// characters are dropped so the key is safe to use as a map key and in logs.
func NormalizeKey(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, "/")
	raw = strings.ToLower(raw)

	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || unicode.IsSpace(r) || r == '/':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	key := strings.Trim(b.String(), "-")
	if len(key) > 128 {
		key = key[:128]
	}
	return key
}

// sanitizeFileName strips any directory components and control characters from
// an uploaded file name, guarding against path traversal.
func sanitizeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r == 0 || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

// contentDisposition builds a Content-Disposition header value that works for
// both ASCII and non-ASCII file names.
func contentDisposition(name string) string {
	asciiName := strings.Map(func(r rune) rune {
		if r > unicode.MaxASCII {
			return '_'
		}
		return r
	}, name)
	asciiName = strings.ReplaceAll(asciiName, `"`, "")
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
		asciiName, url.PathEscape(name))
}

// newBytesReadSeeker returns an io.ReadSeeker over b for use with
// http.ServeContent.
func newBytesReadSeeker(b []byte) io.ReadSeeker {
	return bytes.NewReader(b)
}

// clampRunes truncates s to at most n runes.
func clampRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// sanitizeContentType validates an uploader-declared content type. Anything
// over-long or containing characters that have no business in a MIME type
// (control characters could corrupt response headers) falls back to a plain
// binary type.
func sanitizeContentType(ctype string) string {
	ctype = strings.TrimSpace(ctype)
	if ctype == "" || len(ctype) > 255 {
		return "application/octet-stream"
	}
	for _, r := range ctype {
		if r <= ' ' && r != ' ' || r >= 0x7f {
			return "application/octet-stream"
		}
	}
	return ctype
}

// sameOriginRequest reports whether a state-changing request plausibly comes
// from this site itself. It is a CSRF defence layered on top of the SameSite
// cookie attribute: browsers attach a Sec-Fetch-Site and/or Origin header to
// cross-origin requests, and any request demonstrably sent by another origin
// is rejected. Requests without these headers (non-browser clients, same-origin
// navigations in older browsers) are allowed through.
func sameOriginRequest(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		// fine
	default: // "cross-site" or "same-site" (sibling subdomain)
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
