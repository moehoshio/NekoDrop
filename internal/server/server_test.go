package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moehoshio/NekoDrop/internal/room"
)

func TestNormalizeKey(t *testing.T) {
	cases := map[string]string{
		"Team Cats":   "team-cats",
		"/r/Hello/":   "r-hello",
		"  spaced  ":  "spaced",
		"a__b--c":     "a-b-c",
		"!!!":         "",
		"MixED-CaSe":  "mixed-case",
		"café":        "café",
		"under_score": "under-score",
	}
	for in, want := range cases {
		if got := NormalizeKey(in); got != want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":    "passwd",
		`..\..\win.ini`:       "win.ini",
		"normal.txt":          "normal.txt",
		"":                    "file",
		"..":                  "file",
		"with\x00null.bin":    "withnull.bin",
		"/abs/path/photo.png": "photo.png",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Options{MaxUploadBytes: 1 << 20})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestPostMessageAndDownloadFlow(t *testing.T) {
	s := newTestServer(t)

	// Post a text message.
	body := `{"sender":"alice","text":"hi there"}`
	req := httptest.NewRequest(http.MethodPost, "/api/messages/demo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post message status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var msg room.Message
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if msg.Sender != "alice" || msg.Text != "hi there" {
		t.Fatalf("unexpected message: %+v", msg)
	}

	// Empty text is rejected.
	req = httptest.NewRequest(http.MethodPost, "/api/messages/demo", strings.NewReader(`{"text":"   "}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty text status = %d", rec.Code)
	}
}

func TestUploadAndDownload(t *testing.T) {
	s := newTestServer(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("sender", "bob")
	fw, err := mw.CreateFormFile("file", "../evil/report.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("payload-data")); err != nil {
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
	if msg.Kind != room.KindFile || msg.FileName != "report.txt" {
		t.Fatalf("unexpected file message: %+v (filename should be sanitized)", msg)
	}

	// Download the file.
	dlReq := httptest.NewRequest(http.MethodGet, "/api/files/demo/"+msg.FileID, nil)
	dlRec := httptest.NewRecorder()
	s.ServeHTTP(dlRec, dlReq)
	if dlRec.Code != http.StatusOK {
		t.Fatalf("download status = %d", dlRec.Code)
	}
	if got := dlRec.Body.String(); got != "payload-data" {
		t.Fatalf("download body = %q", got)
	}
	if ct := dlRec.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("download content-type = %q, want application/octet-stream", ct)
	}
	if cd := dlRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("download content-disposition = %q, want attachment", cd)
	}

	// Missing file returns 404.
	missing := httptest.NewRequest(http.MethodGet, "/api/files/demo/nope", nil)
	missRec := httptest.NewRecorder()
	s.ServeHTTP(missRec, missing)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("missing file status = %d", missRec.Code)
	}
}

func TestUploadWithCaptionIsSingleMessage(t *testing.T) {
	s := newTestServer(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("sender", "bob")
	_ = mw.WriteField("text", "look at this @cat#42")
	_ = mw.WriteField("preview", "1")
	fw, err := mw.CreateFormFile("file", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("img-bytes")); err != nil {
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
	// A file sent with a caption is one file message carrying the text, its
	// parsed mentions, and the preview flag.
	if msg.Kind != room.KindFile || msg.FileName != "photo.png" {
		t.Fatalf("unexpected message: %+v", msg)
	}
	if msg.Text != "look at this @cat#42" {
		t.Fatalf("caption not attached: %q", msg.Text)
	}
	if !msg.Preview {
		t.Fatalf("preview flag not set on captioned file")
	}
	if len(msg.Mentions) != 1 || msg.Mentions[0] != "42" {
		t.Fatalf("caption mentions = %v, want [42]", msg.Mentions)
	}
}

func TestUploadTooLarge(t *testing.T) {
	s, err := New(Options{MaxUploadBytes: 8})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "big.bin")
	_, _ = fw.Write(bytes.Repeat([]byte("a"), 1024))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/files/demo", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload status = %d, want 413", rec.Code)
	}
}

func TestIndexAndRoomPagesServed(t *testing.T) {
	s := newTestServer(t)

	for _, path := range []string{"/", "/r/demo", "/static/style.css", "/static/room.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, rec.Code)
		}
	}
}

func TestStreamReplaysHistory(t *testing.T) {
	s := newTestServer(t)
	// Seed a message first.
	rm := s.hub.Room("demo")
	rm.AddText("alice", "100", "earlier", nil, false, "")

	// The SSE handler streams until the request context is cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/stream/demo", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("stream content-type = %q", ct)
	}
	out := rec.Body.String()
	if !strings.Contains(out, "data: ") || !strings.Contains(out, "earlier") {
		t.Fatalf("stream did not replay history: %q", out)
	}
}

// ensure io import is used even if the file evolves.
var _ = io.Discard
