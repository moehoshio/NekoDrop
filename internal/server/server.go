// Package server wires the NekoDrop HTTP API and the static web interface to
// the in-memory room hub. It exposes a chat-room style file-sharing service:
// clients join a room by URL path or join code, exchange text messages, and
// upload files that every other member of the room can download.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/moehoshio/NekoDrop/internal/room"
)

//go:embed web
var webFS embed.FS

// DefaultMaxUploadBytes is the default per-file upload limit (32 MiB).
const DefaultMaxUploadBytes = 32 << 20

// Options configures a Server.
type Options struct {
	// MaxUploadBytes is the maximum accepted size of a single uploaded file.
	// When zero, DefaultMaxUploadBytes is used.
	MaxUploadBytes int64
}

// Server is the NekoDrop HTTP handler.
type Server struct {
	hub       *room.Hub
	mux       *http.ServeMux
	static    fs.FS
	maxUpload int64
}

// New constructs a Server with the given options.
func New(opts Options) (*Server, error) {
	static, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	maxUpload := opts.MaxUploadBytes
	if maxUpload <= 0 {
		maxUpload = DefaultMaxUploadBytes
	}

	s := &Server{
		hub:       room.NewHub(),
		mux:       http.NewServeMux(),
		static:    static,
		maxUpload: maxUpload,
	}
	s.routes()
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /static/", s.handleStatic)
	s.mux.HandleFunc("GET /r/{room}", s.handleRoomPage)
	s.mux.HandleFunc("GET /api/stream/{room}", s.handleStream)
	s.mux.HandleFunc("POST /api/messages/{room}", s.handlePostMessage)
	s.mux.HandleFunc("POST /api/files/{room}", s.handleUpload)
	s.mux.HandleFunc("GET /api/files/{room}/{id}", s.handleDownload)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.serveFile(w, r, "index.html", "text/html; charset=utf-8")
}

func (s *Server) handleRoomPage(w http.ResponseWriter, r *http.Request) {
	if NormalizeKey(r.PathValue("room")) == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.serveFile(w, r, "room.html", "text/html; charset=utf-8")
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	if name == "" || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	ctype := ""
	switch {
	case strings.HasSuffix(name, ".css"):
		ctype = "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		ctype = "text/javascript; charset=utf-8"
	}
	s.serveFile(w, r, "static/"+name, ctype)
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name, ctype string) {
	data, err := fs.ReadFile(s.static, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	_, _ = w.Write(data)
}

// handleStream serves the room's message history followed by live updates over
// Server-Sent Events.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid room", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rm := s.hub.Room(key)
	history, ch, cancel := rm.Subscribe()
	defer cancel()

	for _, m := range history {
		if err := writeSSE(w, m); err != nil {
			return
		}
	}
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSE(w, m); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, m room.Message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", payload)
	return err
}

type postMessageRequest struct {
	Sender string `json:"sender"`
	Text   string `json:"text"`
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid room", http.StatusBadRequest)
		return
	}

	var req postMessageRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "message text is required", http.StatusBadRequest)
		return
	}

	rm := s.hub.Room(key)
	m := rm.AddText(sanitizeSender(req.Sender), text)
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid room", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(s.maxUpload + (1 << 20)); err != nil {
		http.Error(w, "file too large or malformed upload", http.StatusRequestEntityTooLarge)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, s.maxUpload+1))
	if err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}
	if int64(len(data)) > s.maxUpload {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	name := sanitizeFileName(header.Filename)
	ctype := header.Header.Get("Content-Type")
	if ctype == "" {
		ctype = "application/octet-stream"
	}

	rm := s.hub.Room(key)
	m := rm.AddFile(sanitizeSender(r.FormValue("sender")), name, ctype, data)
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid room", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")

	rm := s.hub.Room(key)
	f, err := rm.File(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Force a download with the original name to avoid the file being
	// interpreted/executed by the browser (e.g. inline HTML/JS).
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentDisposition(f.Name))
	http.ServeContent(w, r, f.Name, time.Time{}, newBytesReadSeeker(f.Data))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
