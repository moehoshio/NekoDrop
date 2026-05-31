package server

import (
	"bytes"
	"fmt"
	"io"
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

// sanitizeSender trims a display name and applies a sane default and length
// limit. The returned value is later rendered as text on the client, never as
// HTML.
func sanitizeSender(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "anonymous"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
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
