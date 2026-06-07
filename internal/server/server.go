// Package server wires the NekoDrop HTTP API and the static web interface to
// the in-memory channel hub. It exposes a chat-room style file-sharing service
// with lightweight identities (each visitor gets a stable "name#uid"), public
// and private channels, an ownership and moderation model, announcements,
// @-mentions, inline media previews and live presence.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/moehoshio/NekoDrop/internal/room"
	"github.com/moehoshio/NekoDrop/internal/storage"
	"github.com/moehoshio/NekoDrop/internal/user"
)

//go:embed web
var webFS embed.FS

// DefaultMaxUploadBytes is the default per-file upload limit (32 MiB).
const DefaultMaxUploadBytes = 32 << 20

// cookieName is the name of the cookie holding a visitor's identity token.
const cookieName = "nekodrop_token"

// Options configures a Server.
type Options struct {
	// MaxUploadBytes is the maximum accepted size of a single uploaded file.
	// When zero, DefaultMaxUploadBytes is used.
	MaxUploadBytes int64
	// MaxChannelsPerUser caps how many channels a single user may own at once.
	// Zero or negative means unlimited.
	MaxChannelsPerUser int
	// Store is the durable persistence backend. When nil, a non-persistent
	// in-memory store is used and the server keeps its original behaviour.
	Store storage.Store
}

// Server is the NekoDrop HTTP handler.
type Server struct {
	hub       *room.Hub
	users     *user.Registry
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
	store := opts.Store
	if store == nil {
		store = storage.NewMemory()
	}

	s := &Server{
		hub:       room.NewHubWithStore(opts.MaxChannelsPerUser, store),
		users:     user.NewRegistryWithStore(store),
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

	// Identity.
	s.mux.HandleFunc("GET /api/me", s.handleMe)
	s.mux.HandleFunc("POST /api/me", s.handleRename)

	// Channel directory & lifecycle.
	s.mux.HandleFunc("GET /api/channels", s.handleChannelList)
	s.mux.HandleFunc("POST /api/channels", s.handleCreateChannel)
	s.mux.HandleFunc("GET /api/channels/{room}", s.handleChannelInfo)
	s.mux.HandleFunc("PATCH /api/channels/{room}", s.handleUpdateChannel)
	s.mux.HandleFunc("DELETE /api/channels/{room}", s.handleDissolveChannel)
	s.mux.HandleFunc("POST /api/channels/{room}/join", s.handleJoin)
	s.mux.HandleFunc("POST /api/channels/{room}/leave", s.handleLeave)
	s.mux.HandleFunc("POST /api/channels/{room}/moderate", s.handleModerate)
	s.mux.HandleFunc("POST /api/channels/{room}/announcements", s.handleAnnounce)

	// Messaging & files.
	s.mux.HandleFunc("GET /api/stream/{room}", s.handleStream)
	s.mux.HandleFunc("POST /api/messages/{room}", s.handlePostMessage)
	s.mux.HandleFunc("POST /api/files/{room}", s.handleUpload)
	s.mux.HandleFunc("GET /api/files/{room}/{id}", s.handleDownload)
}

// --- Identity helpers ---

// currentUser returns the visitor's identity, minting one (and setting the
// identity cookie) on first contact.
func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) *user.User {
	if c, err := r.Cookie(cookieName); err == nil {
		if u := s.users.Get(c.Value); u != nil {
			return u
		}
	}
	u := s.users.Create("")
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    u.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 365,
	})
	return u
}

// resolveSender returns the current user, applying an optional display-name
// change supplied with the request. The UID never changes.
func (s *Server) resolveSender(w http.ResponseWriter, r *http.Request, name string) *user.User {
	u := s.currentUser(w, r)
	if strings.TrimSpace(name) != "" && user.SanitizeName(name) != u.Name {
		if updated, ok := s.users.Rename(u.Token, name); ok {
			return updated
		}
	}
	return u
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentUser(w, r))
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	u := s.resolveSender(w, r, req.Name)
	writeJSON(w, http.StatusOK, u)
}

// --- Static pages ---

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

// --- Channel directory & lifecycle ---

func (s *Server) handleChannelList(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(w, r) // ensure the visitor has an identity cookie
	writeJSON(w, http.StatusOK, map[string]any{
		"channels": s.hub.PublicList(),
		"mine":     s.hub.OwnedBy(u.UID),
	})
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(w, r)

	var req struct {
		Key             string `json:"key"`
		Name            string `json:"name"`
		Description     string `json:"description"`
		Visibility      string `json:"visibility"`
		ListPublic      bool   `json:"listPublic"`
		AllowJoin       *bool  `json:"allowJoin"`
		RequireApproval bool   `json:"requireApproval"`
		AllowSpeak      *bool  `json:"allowSpeak"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	key := NormalizeKey(req.Key)
	if key == "" {
		key = NormalizeKey(req.Name)
	}
	if key == "" {
		http.Error(w, "a channel key or name is required", http.StatusBadRequest)
		return
	}

	vis := room.Public
	if req.Visibility == string(room.Private) {
		vis = room.Private
	}
	allowJoin := true
	if req.AllowJoin != nil {
		allowJoin = *req.AllowJoin
	}
	allowSpeak := true
	if req.AllowSpeak != nil {
		allowSpeak = *req.AllowSpeak
	}

	rm, err := s.hub.Create(key, u.UID, room.Settings{
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		Visibility:      vis,
		ListPublic:      req.ListPublic,
		AllowJoin:       allowJoin,
		RequireApproval: req.RequireApproval,
		AllowSpeak:      allowSpeak,
	})
	if err != nil {
		switch err {
		case room.ErrChannelExists:
			http.Error(w, "that channel key is already in use", http.StatusConflict)
		case room.ErrLimitReached:
			http.Error(w, "you have reached your channel creation limit", http.StatusForbidden)
		default:
			http.Error(w, "could not create channel", http.StatusBadRequest)
		}
		return
	}
	writeJSON(w, http.StatusCreated, rm.Info())
}

// channelView is the payload returned for a single channel: its metadata, the
// viewer's role, and (for admins) any pending join requests.
type channelView struct {
	Channel room.Info            `json:"channel"`
	Role    room.Role            `json:"role"`
	Pending []room.PendingMember `json:"pending,omitempty"`
	Members []room.Member        `json:"members,omitempty"`
}

func (s *Server) handleChannelInfo(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}
	u := s.currentUser(w, r)

	rm, ok := s.hub.Lookup(key)
	if !ok {
		// Not yet created: present the channel as the open, ownerless room it
		// would become on first message, without actually creating it.
		writeJSON(w, http.StatusOK, channelView{
			Channel: defaultInfo(key),
			Role:    room.Role{UID: u.UID, CanRead: true, CanSpeak: true},
		})
		return
	}
	view := channelView{Channel: rm.Info(), Role: rm.RoleOf(u.UID)}
	if rm.IsAdmin(u.UID) {
		view.Pending = rm.Pending()
		view.Members = rm.Members()
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	rm, u, ok := s.lookupForAction(w, r)
	if !ok {
		return
	}
	cur := rm.Info()
	req := struct {
		Name            *string `json:"name"`
		Description     *string `json:"description"`
		Visibility      *string `json:"visibility"`
		ListPublic      *bool   `json:"listPublic"`
		AllowJoin       *bool   `json:"allowJoin"`
		RequireApproval *bool   `json:"requireApproval"`
		AllowSpeak      *bool   `json:"allowSpeak"`
	}{}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	// Start from current settings and apply only the supplied fields.
	next := room.Settings{
		Name:            cur.Name,
		Description:     cur.Description,
		Visibility:      cur.Visibility,
		ListPublic:      cur.ListPublic,
		AllowJoin:       cur.AllowJoin,
		RequireApproval: cur.RequireApproval,
		AllowSpeak:      cur.AllowSpeak,
	}
	if req.Name != nil {
		next.Name = *req.Name
	}
	if req.Description != nil {
		next.Description = *req.Description
	}
	if req.Visibility != nil {
		next.Visibility = room.Visibility(*req.Visibility)
	}
	if req.ListPublic != nil {
		next.ListPublic = *req.ListPublic
	}
	if req.AllowJoin != nil {
		next.AllowJoin = *req.AllowJoin
	}
	if req.RequireApproval != nil {
		next.RequireApproval = *req.RequireApproval
	}
	if req.AllowSpeak != nil {
		next.AllowSpeak = *req.AllowSpeak
	}

	info, err := rm.Update(u.UID, next)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDissolveChannel(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}
	u := s.currentUser(w, r)
	if err := s.hub.Dissolve(key, u.UID); err != nil {
		status := http.StatusForbidden
		if err == room.ErrChannelNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}
	u := s.currentUser(w, r)
	rm := s.hub.Room(key)
	pending, err := rm.Join(u.UID, u.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pending": pending, "role": rm.RoleOf(u.UID)})
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}
	u := s.currentUser(w, r)
	if rm, ok := s.hub.Lookup(key); ok {
		rm.Leave(u.UID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleModerate(w http.ResponseWriter, r *http.Request) {
	rm, u, ok := s.lookupForAction(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
		UID    string `json:"uid"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := rm.Moderate(u.UID, req.Action, req.UID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"channel": rm.Info(),
		"pending": rm.Pending(),
		"members": rm.Members(),
	})
}

func (s *Server) handleAnnounce(w http.ResponseWriter, r *http.Request) {
	rm, u, ok := s.lookupForAction(w, r)
	if !ok {
		return
	}
	if !rm.IsAdmin(u.UID) {
		http.Error(w, "only the owner or an admin can post announcements", http.StatusForbidden)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "announcement text is required", http.StatusBadRequest)
		return
	}
	if len([]rune(text)) > 1000 {
		text = string([]rune(text)[:1000])
	}
	a := rm.AddAnnouncement(u.UID, u.Name, text)
	writeJSON(w, http.StatusCreated, a)
}

// lookupForAction resolves the channel for a moderation/settings action. It
// requires the channel to already exist and writes an error response when it
// does not. Authorization is enforced by the called Room method.
func (s *Server) lookupForAction(w http.ResponseWriter, r *http.Request) (*room.Room, *user.User, bool) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return nil, nil, false
	}
	rm, ok := s.hub.Lookup(key)
	if !ok {
		http.Error(w, "channel not found", http.StatusNotFound)
		return nil, nil, false
	}
	return rm, s.currentUser(w, r), true
}

// --- Messaging & files ---

// handleStream serves the channel's history and announcements followed by live
// events over Server-Sent Events. Private channels require read access.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	u := s.currentUser(w, r)
	rm := s.hub.Room(key)
	if !rm.CanRead(u.UID) {
		http.Error(w, "this channel is private; join to view it", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	history, announcements, ch, cancel := rm.Subscribe(u.UID)
	defer cancel()

	for _, a := range announcements {
		a := a
		if err := writeSSE(w, room.Event{Type: room.EventAnnouncement, Announcement: &a}); err != nil {
			return
		}
	}
	for _, m := range history {
		m := m
		if err := writeSSE(w, room.Event{Type: room.EventMessage, Message: &m}); err != nil {
			return
		}
	}
	if err := writeSSE(w, room.Event{Type: room.EventPresence, Online: rm.OnlineCount()}); err != nil {
		return
	}
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSE(w, e); err != nil {
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

func writeSSE(w io.Writer, e room.Event) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", payload)
	return err
}

type postMessageRequest struct {
	Sender  string `json:"sender"`
	Text    string `json:"text"`
	Preview bool   `json:"preview"`
}

func (s *Server) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}

	var req postMessageRequest
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "message text is required", http.StatusBadRequest)
		return
	}

	u := s.resolveSender(w, r, req.Sender)
	rm := s.hub.Room(key)
	if !rm.CanSpeak(u.UID) {
		http.Error(w, speakDeniedReason(rm, u.UID), http.StatusForbidden)
		return
	}
	m := rm.AddText(u.Name, u.UID, text, parseMentions(text), req.Preview)
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(s.maxUpload + (1 << 20)); err != nil {
		http.Error(w, "file too large or malformed upload", http.StatusRequestEntityTooLarge)
		return
	}

	u := s.resolveSender(w, r, r.FormValue("sender"))
	rm := s.hub.Room(key)
	if !rm.CanSpeak(u.UID) {
		http.Error(w, speakDeniedReason(rm, u.UID), http.StatusForbidden)
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

	m := rm.AddFile(u.Name, u.UID, name, ctype, data)
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")

	u := s.currentUser(w, r)
	rm, ok := s.hub.Lookup(key)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !rm.CanRead(u.UID) {
		http.Error(w, "this channel is private; join to view it", http.StatusForbidden)
		return
	}
	f, err := rm.File(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("X-Content-Type-Options", "nosniff")

	// Media previews: when explicitly requested (?inline=1) and the declared
	// content type is a whitelisted media type, serve the file inline with its
	// own type so the browser can render it in the chat. nosniff guarantees the
	// browser honours the declared type exactly, so it can never be coerced into
	// executing the payload as HTML/JS. Everything else is forced to download.
	if r.URL.Query().Get("inline") == "1" && isInlineMedia(f.ContentType) {
		w.Header().Set("Content-Type", f.ContentType)
		w.Header().Set("Content-Disposition", "inline")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", contentDisposition(f.Name))
	}
	http.ServeContent(w, r, f.Name, time.Time{}, newBytesReadSeeker(f.Data))
}

// isInlineMedia reports whether a declared content type is safe to render
// inline (image, video or audio). Combined with nosniff this is safe even
// though the type is supplied by the uploader.
func isInlineMedia(ctype string) bool {
	ctype = strings.ToLower(strings.TrimSpace(ctype))
	if i := strings.IndexByte(ctype, ';'); i >= 0 {
		ctype = strings.TrimSpace(ctype[:i])
	}
	return strings.HasPrefix(ctype, "image/") ||
		strings.HasPrefix(ctype, "video/") ||
		strings.HasPrefix(ctype, "audio/")
}

// speakDeniedReason returns a human-readable explanation for why a user may not
// speak in a channel.
func speakDeniedReason(rm *room.Room, uid string) string {
	role := rm.RoleOf(uid)
	switch {
	case role.Banned:
		return "you are banned from this channel"
	case role.Muted:
		return "you have been muted in this channel"
	case !role.Member:
		return "join this channel before sending messages"
	default:
		return "sending is currently disabled in this channel"
	}
}

// mentionRe matches an @-mention of the form "@anything#123", capturing the
// numeric UID. It mirrors how the client renders mentions.
var mentionRe = regexp.MustCompile(`@[^\s#@]+#(\d+)`)

// parseMentions extracts the unique UIDs mentioned in text.
func parseMentions(text string) []string {
	matches := mentionRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		uid := m[1]
		if !seen[uid] {
			seen[uid] = true
			out = append(out, uid)
		}
	}
	return out
}

// defaultInfo describes a not-yet-created channel as the open, ownerless room it
// would become on first use.
func defaultInfo(key string) room.Info {
	return room.Info{
		Key:        key,
		Name:       key,
		Visibility: room.Public,
		AllowJoin:  true,
		AllowSpeak: true,
	}
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
