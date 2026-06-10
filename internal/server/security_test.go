package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/moehoshio/NekoDrop/internal/room"
)

// uploadWithType posts a file with an explicit declared content type and
// returns the resulting message.
func uploadWithType(t *testing.T, s *Server, name, ctype, data string) room.Message {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	h.Set("Content-Type", ctype)
	fw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/files/demo", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var msg room.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return msg
}

// An uploaded SVG must never be rendered inline at this origin: SVG documents
// can embed script, so serving one inline would be stored XSS.
func TestSVGNeverServedInline(t *testing.T) {
	s := newTestServer(t)
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	msg := uploadWithType(t, s, "evil.svg", "image/svg+xml", svg)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/files/demo/"+msg.FileID+"?inline=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("download status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("svg inline content-type = %q, want application/octet-stream", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("svg inline disposition = %q, want attachment", cd)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("download CSP = %q, want sandbox", csp)
	}
}

// A genuine raster image is still rendered inline when requested.
func TestRasterImageServedInline(t *testing.T) {
	s := newTestServer(t)
	msg := uploadWithType(t, s, "cat.png", "image/png", "png-bytes")

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/files/demo/"+msg.FileID+"?inline=1", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("png inline content-type = %q, want image/png", ct)
	}
	if nosniff := rec.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", nosniff)
	}
}

func TestIsInlineMedia(t *testing.T) {
	cases := map[string]bool{
		"image/png":                true,
		"image/jpeg; charset=bin":  true,
		"video/mp4":                true,
		"audio/mpeg":               true,
		"image/svg+xml":            false,
		"IMAGE/SVG+XML":            false,
		"image/svg+xml;charset=x":  false,
		"text/html":                false,
		"application/xhtml+xml":    false,
		"application/octet-stream": false,
	}
	for in, want := range cases {
		if got := isInlineMedia(in); got != want {
			t.Errorf("isInlineMedia(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSanitizeContentType(t *testing.T) {
	cases := map[string]string{
		"image/png":              "image/png",
		"":                       "application/octet-stream",
		"text/plain\r\nX-Evil:":  "application/octet-stream",
		strings.Repeat("a", 300): "application/octet-stream",
	}
	for in, want := range cases {
		if got := sanitizeContentType(in); got != want {
			t.Errorf("sanitizeContentType(%q) = %q, want %q", in, got, want)
		}
	}
}

// State-changing requests carrying cross-site browser metadata are rejected.
func TestCrossOriginWritesRejected(t *testing.T) {
	s := newTestServer(t)
	body := `{"sender":"alice","text":"hi"}`

	// Mismatched Origin header → rejected.
	req := httptest.NewRequest(http.MethodPost, "/api/messages/demo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST status = %d, want 403", rec.Code)
	}

	// Cross-site fetch metadata → rejected even without Origin.
	req = httptest.NewRequest(http.MethodPost, "/api/messages/demo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST status = %d, want 403", rec.Code)
	}

	// Matching Origin → allowed.
	req = httptest.NewRequest(http.MethodPost, "/api/messages/demo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("same-origin POST status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Cross-origin GETs (reads) are unaffected.
	req = httptest.NewRequest(http.MethodGet, "/api/channels", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cross-origin GET status = %d, want 200", rec.Code)
	}
}

// Every response carries the baseline security headers; HTML pages also get a
// Content-Security-Policy.
func TestSecurityHeaders(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d", rec.Code)
	}
	h := rec.Header()
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := h.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if got := h.Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	csp := h.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("page CSP = %q", csp)
	}
}

// Message text is length-bounded so one request cannot pin ~1 MiB of text in
// every channel's resident history.
func TestMessageTextClamped(t *testing.T) {
	s := newTestServer(t)
	long := strings.Repeat("x", maxMessageRunes+500)
	body, _ := json.Marshal(map[string]string{"sender": "alice", "text": long})
	req := httptest.NewRequest(http.MethodPost, "/api/messages/demo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post status = %d", rec.Code)
	}
	var msg room.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(msg.Text)); got != maxMessageRunes {
		t.Fatalf("stored text length = %d, want %d", got, maxMessageRunes)
	}
}

// The identity cookie is marked Secure when the request arrived over TLS
// (here signalled by a reverse proxy).
func TestCookieSecureOverTLS(t *testing.T) {
	s := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no identity cookie set")
	}
	c := cookies[0]
	if !c.Secure || !c.HttpOnly {
		t.Fatalf("cookie Secure=%v HttpOnly=%v, want both true", c.Secure, c.HttpOnly)
	}
}
