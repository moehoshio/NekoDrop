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

// maxMessageRunes caps the length of a message text or file caption. Without a
// cap a single request (bounded only by the 1 MiB JSON body limit) could pin a
// megabyte of text per message in every channel's resident history. Long-form
// pastes fit comfortably; the per-channel byte budget (room.Limits.
// MaxBytesPerChannel) bounds the aggregate.
const maxMessageRunes = 16384

// maxAnnouncementRunes caps the length of an announcement.
const maxAnnouncementRunes = 1000

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
	// Limits bound in-memory resource usage. Zero-valued fields fall back to
	// room.DefaultLimits.
	Limits room.Limits
	// MaxUsers caps identities retained in memory (0 = default).
	MaxUsers int
	// AdminEnabled turns on the administrator panel at /admin. It additionally
	// requires a non-empty AdminToken; both default to off.
	AdminEnabled bool
	// AdminToken is the secret an administrator presents to use the panel.
	AdminToken string
}

// Server is the NekoDrop HTTP handler.
type Server struct {
	hub    *room.Hub
	users  *user.Registry
	mux    *http.ServeMux
	static fs.FS
	store  storage.Store
	admin  *adminPanel

	// Base configuration as resolved from file/env/flags. The admin panel's
	// runtime overrides layer on top of these (see admin.go).
	baseMaxUpload          int64
	baseMaxChannelsPerUser int
	baseLimits             room.Limits
	baseMaxUsers           int

	// startTime anchors the uptime figure on the admin dashboard.
	startTime time.Time
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
		hub:                    room.NewHubWithStore(opts.MaxChannelsPerUser, store, opts.Limits),
		users:                  user.NewRegistryWithStore(store, opts.MaxUsers),
		mux:                    http.NewServeMux(),
		static:                 static,
		store:                  store,
		admin:                  newAdminPanel(opts.AdminEnabled, opts.AdminToken, store),
		baseMaxUpload:          maxUpload,
		baseMaxChannelsPerUser: opts.MaxChannelsPerUser,
		baseLimits:             opts.Limits,
		baseMaxUsers:           opts.MaxUsers,
		startTime:              time.Now(),
	}
	// Re-apply any persisted runtime overrides from a previous run.
	s.applyRuntimeConfig()
	s.routes()
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")

	// Reject state-changing requests that demonstrably come from another
	// origin. Together with the SameSite cookie attribute this blocks CSRF.
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		if !sameOriginRequest(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// pageCSP is the Content-Security-Policy applied to the HTML pages. Scripts
// and styles may only load from this origin; images and media may additionally
// load from https (sender-chosen inline link previews) and blob: (staged
// attachment thumbnails). frame-ancestors blocks clickjacking.
const pageCSP = "default-src 'self'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' https: data: blob:; media-src 'self' https: blob:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'self'; " +
	"form-action 'self'; frame-ancestors 'none'"

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleIndex)
	s.mux.HandleFunc("GET /static/", s.handleStatic)
	s.mux.HandleFunc("GET /r/{room}", s.handleRoomPage)

	// Identity.
	s.mux.HandleFunc("GET /api/me", s.handleMe)
	s.mux.HandleFunc("POST /api/me", s.handleRename)

	// Account migration (per-user opt-in, off by default).
	s.mux.HandleFunc("GET /api/me/migration", s.handleMigrationStatus)
	s.mux.HandleFunc("POST /api/me/migration", s.handleMigrationEnable)
	s.mux.HandleFunc("DELETE /api/me/migration", s.handleMigrationDisable)
	s.mux.HandleFunc("POST /api/migrate", s.handleMigrate)

	// Admin panel (served only when enabled in the configuration).
	s.mux.HandleFunc("GET /admin", s.handleAdminPage)
	s.mux.HandleFunc("GET /api/admin/overview", s.handleAdminOverview)
	s.mux.HandleFunc("GET /api/admin/channels", s.handleAdminChannels)
	s.mux.HandleFunc("PATCH /api/admin/channels/{id}", s.handleAdminPatchChannel)
	s.mux.HandleFunc("GET /api/admin/users", s.handleAdminUsers)
	s.mux.HandleFunc("PATCH /api/admin/users/{uid}", s.handleAdminPatchUser)
	s.mux.HandleFunc("GET /api/admin/config", s.handleAdminGetConfig)
	s.mux.HandleFunc("PATCH /api/admin/config", s.handleAdminPatchConfig)

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
	s.mux.HandleFunc("PATCH /api/channels/{room}/announcements/{id}", s.handleEditAnnouncement)
	s.mux.HandleFunc("DELETE /api/channels/{room}/announcements/{id}", s.handleDeleteAnnouncement)

	// Messaging & files.
	s.mux.HandleFunc("GET /api/stream/{room}", s.handleStream)
	s.mux.HandleFunc("POST /api/messages/{room}", s.handlePostMessage)
	s.mux.HandleFunc("PATCH /api/messages/{room}/{id}", s.handleEditMessage)
	s.mux.HandleFunc("DELETE /api/messages/{room}/{id}", s.handleDeleteMessage)
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
	s.setIdentityCookie(w, r, u.Token)
	return u
}

// setIdentityCookie binds the browser to the identity behind token.
func (s *Server) setIdentityCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		// Mark the identity cookie Secure whenever the request arrived over
		// TLS (directly or via a reverse proxy) so it is never replayed over
		// plaintext HTTP.
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 365,
	})
}

// cookieUID returns the UID of the visitor's existing identity without minting
// a new one. Used where the identity is only needed for a policy lookup before
// the request body is consumed (e.g. resolving the upload size limit).
func (s *Server) cookieUID(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		if u := s.users.Get(c.Value); u != nil {
			return u.UID
		}
	}
	return ""
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

// wireName is the display name attached to a user's messages, announcements and
// roster entries. It is the name the user deliberately chose, or an empty string
// while they are still on the auto-assigned default. Clients render the empty
// case as a localized "guest" placeholder, so the default name is translated per
// UI language — without ever translating a name a user actually typed, even if
// they literally chose "Guest".
func wireName(u *user.User) string {
	if u == nil || !u.Named {
		return ""
	}
	return u.Name
}

// meJSON is the identity payload returned by the /api/me endpoints. It exposes
// whether account migration is enabled but never the codes themselves.
func (s *Server) meJSON(u *user.User) map[string]any {
	return map[string]any{
		"uid": u.UID,
		// An unnamed visitor's name is sent empty so the client renders a
		// localized "guest" placeholder; a chosen name is sent verbatim.
		"name":      wireName(u),
		"named":     u.Named,
		"migration": s.users.MigrationCode(u.Token) != "",
	}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.meJSON(s.currentUser(w, r)))
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
	writeJSON(w, http.StatusOK, s.meJSON(u))
}

// --- Account migration ---
//
// Migration lets a user carry their identity to another browser: they opt in
// (off by default) to mint a persistent migration code, then enter that code
// in the other browser, whose identity cookie is re-bound to this account.

func (s *Server) handleMigrationStatus(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(w, r)
	code := s.users.MigrationCode(u.Token)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": code != "", "code": code})
}

func (s *Server) handleMigrationEnable(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(w, r)
	code, ok := s.users.EnableMigration(u.Token)
	if !ok {
		http.Error(w, "unknown identity", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "code": code})
}

func (s *Server) handleMigrationDisable(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(w, r)
	s.users.DisableMigration(u.Token)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "code": ""})
}

// handleMigrate adopts the account behind a migration code on this browser:
// the identity cookie is replaced with the target account's token. The
// previous identity of this browser (if any) is simply left behind.
func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	target := s.users.ByMigrationCode(strings.TrimSpace(req.Code))
	if target == nil {
		http.Error(w, "unknown migration code", http.StatusNotFound)
		return
	}
	s.setIdentityCookie(w, r, target.Token)
	writeJSON(w, http.StatusOK, s.meJSON(target))
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
	if strings.HasPrefix(ctype, "text/html") {
		w.Header().Set("Content-Security-Policy", pageCSP)
	}
	_, _ = w.Write(data)
}

// --- Channel directory & lifecycle ---

func (s *Server) handleChannelList(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(w, r) // ensure the visitor has an identity cookie
	writeJSON(w, http.StatusOK, map[string]any{
		"channels": s.withoutBannedChannels(s.hub.PublicList()),
		"mine":     s.withoutBannedChannels(s.hub.OwnedBy(u.UID)),
		"joined":   s.withoutBannedChannels(s.hub.JoinedBy(u.UID)),
	})
}

// withoutBannedChannels drops channels an administrator has disabled from a
// user-facing listing.
func (s *Server) withoutBannedChannels(in []room.Info) []room.Info {
	out := in[:0]
	for _, info := range in {
		if !s.admin.channelBanned(info.ID) {
			out = append(out, info)
		}
	}
	return out
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	u := s.currentUser(w, r)
	if s.admin.userBanned(u.UID) {
		http.Error(w, bannedUserMsg, http.StatusForbidden)
		return
	}

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

	// Apply the same length bounds Room.Update enforces on later edits.
	rm, err := s.hub.Create(key, u.UID, room.Settings{
		Name:            clampRunes(strings.TrimSpace(req.Name), 64),
		Description:     clampRunes(strings.TrimSpace(req.Description), 280),
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
// viewer's role, and (for admins) any pending join requests. Disabled reports
// that a server administrator has taken the channel out of service.
type channelView struct {
	Channel  room.Info            `json:"channel"`
	Role     room.Role            `json:"role"`
	Disabled bool                 `json:"disabled,omitempty"`
	Pending  []room.PendingMember `json:"pending,omitempty"`
	Members  []room.Member        `json:"members,omitempty"`
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
	if s.admin.channelBanned(view.Channel.ID) {
		// Disabled channels expose only their identity, not roster or requests.
		view.Disabled = true
		writeJSON(w, http.StatusOK, view)
		return
	}
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
	if rm, ok := s.hub.Lookup(key); ok && s.admin.channelBanned(rm.ID) {
		http.Error(w, bannedChannelMsg, http.StatusForbidden)
		return
	}
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
	if s.admin.userBanned(u.UID) {
		http.Error(w, bannedUserMsg, http.StatusForbidden)
		return
	}
	rm := s.hub.Room(key)
	if s.admin.channelBanned(rm.ID) {
		http.Error(w, bannedChannelMsg, http.StatusForbidden)
		return
	}
	pending, err := rm.Join(u.UID, wireName(u))
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
		// PurgeMessages, valid with the "ban" action, additionally deletes every
		// message the banned member ever sent in this channel.
		PurgeMessages bool `json:"purgeMessages"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := rm.Moderate(u.UID, req.Action, req.UID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if req.Action == "ban" && req.PurgeMessages {
		if _, err := rm.PurgeMessagesBy(u.UID, req.UID); err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
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
	text = clampRunes(text, maxAnnouncementRunes)
	a := rm.AddAnnouncement(u.UID, wireName(u), text)
	writeJSON(w, http.StatusCreated, a)
}

// handleEditAnnouncement lets an owner or admin rewrite an announcement.
func (s *Server) handleEditAnnouncement(w http.ResponseWriter, r *http.Request) {
	rm, u, ok := s.lookupForAction(w, r)
	if !ok {
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
	text = clampRunes(text, maxAnnouncementRunes)
	a, err := rm.EditAnnouncement(u.UID, r.PathValue("id"), text)
	if err != nil {
		status := http.StatusForbidden
		if err == room.ErrAnnouncementNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// handleDeleteAnnouncement lets an owner or admin remove an announcement.
func (s *Server) handleDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	rm, u, ok := s.lookupForAction(w, r)
	if !ok {
		return
	}
	if err := rm.DeleteAnnouncement(u.UID, r.PathValue("id")); err != nil {
		status := http.StatusForbidden
		if err == room.ErrAnnouncementNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// lookupForAction resolves the channel for a moderation/settings action. It
// requires the channel to already exist and writes an error response when it
// does not. Site-wide administrator bans (of the channel or the acting user)
// are rejected here, since every caller is a state-changing action;
// channel-level authorization is enforced by the called Room method.
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
	if s.admin.channelBanned(rm.ID) {
		http.Error(w, bannedChannelMsg, http.StatusForbidden)
		return nil, nil, false
	}
	u := s.currentUser(w, r)
	if s.admin.userBanned(u.UID) {
		http.Error(w, bannedUserMsg, http.StatusForbidden)
		return nil, nil, false
	}
	return rm, u, true
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
	if s.admin.channelBanned(rm.ID) {
		http.Error(w, bannedChannelMsg, http.StatusForbidden)
		return
	}
	if !rm.CanRead(u.UID) {
		http.Error(w, "this channel is private; join to view it", http.StatusForbidden)
		return
	}
	// Shed load before opening a new stream so a flood of connections to one
	// channel cannot exhaust goroutines and memory.
	if rm.SubscriberLimitReached() {
		http.Error(w, "this channel has too many active connections; try again shortly", http.StatusServiceUnavailable)
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
		// Re-resolve the sender's display name against the channel's current
		// nicknames so replayed history reflects later nickname changes rather
		// than the name captured when the message was first sent.
		m.Sender = rm.DisplayName(m.SenderUID, m.Sender)
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
	Nick    string `json:"nick"`
	Text    string `json:"text"`
	ReplyTo string `json:"replyTo"`
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
	text = clampRunes(text, maxMessageRunes)

	u := s.resolveSender(w, r, req.Sender)
	if s.admin.userBanned(u.UID) {
		http.Error(w, bannedUserMsg, http.StatusForbidden)
		return
	}
	rm := s.hub.Room(key)
	if s.admin.channelBanned(rm.ID) {
		http.Error(w, bannedChannelMsg, http.StatusForbidden)
		return
	}
	if !rm.CanSpeak(u.UID) {
		http.Error(w, speakDeniedReason(rm, u.UID), http.StatusForbidden)
		return
	}
	// Resolve the display name from the channel nickname (when set), falling back
	// to the global name. Messages are keyed by UID, so a later nickname change
	// retroactively relabels this user's history. Nicknames pass through the same
	// sanitizer as global names (length cap, no smuggled whitespace).
	display := rm.ApplyNick(u.UID, wireName(u), user.SanitizeName(req.Nick))
	m := rm.AddText(display, u.UID, text, parseMentions(text), req.Preview, req.ReplyTo)
	writeJSON(w, http.StatusCreated, m)
}

// handleEditMessage lets a sender rewrite the text of their own message.
func (s *Server) handleEditMessage(w http.ResponseWriter, r *http.Request) {
	rm, u, ok := s.lookupForAction(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	text := clampRunes(strings.TrimSpace(req.Text), maxMessageRunes)
	m, err := rm.EditMessage(u.UID, id, text, parseMentions(text))
	if err != nil {
		status := http.StatusForbidden
		if err == room.ErrMessageNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// handleDeleteMessage removes a message: senders their own, admins anyone's.
func (s *Server) handleDeleteMessage(w http.ResponseWriter, r *http.Request) {
	rm, u, ok := s.lookupForAction(w, r)
	if !ok {
		return
	}
	if err := rm.DeleteMessage(u.UID, r.PathValue("id")); err != nil {
		status := http.StatusForbidden
		if err == room.ErrMessageNotFound {
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	key := NormalizeKey(r.PathValue("room"))
	if key == "" {
		http.Error(w, "invalid channel", http.StatusBadRequest)
		return
	}

	// Resolve the effective upload limit before touching the body: an
	// administrator may have granted this user or channel an exemption from
	// (or a tighter bound than) the configured limit.
	channelID := ""
	if existing, ok := s.hub.Lookup(key); ok {
		if s.admin.channelBanned(existing.ID) {
			http.Error(w, bannedChannelMsg, http.StatusForbidden)
			return
		}
		channelID = existing.ID
	}
	maxUpload := s.admin.uploadLimit(s.baseMaxUpload, s.cookieUID(r), channelID)

	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err := r.ParseMultipartForm(maxUpload + (1 << 20)); err != nil {
		http.Error(w, "file too large or malformed upload", http.StatusRequestEntityTooLarge)
		return
	}

	u := s.resolveSender(w, r, r.FormValue("sender"))
	if s.admin.userBanned(u.UID) {
		http.Error(w, bannedUserMsg, http.StatusForbidden)
		return
	}
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

	data, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}
	if int64(len(data)) > maxUpload {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}

	name := sanitizeFileName(header.Filename)
	ctype := sanitizeContentType(header.Header.Get("Content-Type"))

	// An optional caption lets a file be sent together with a describing message
	// as a single entry. Inline previews are opt-in, mirroring text messages.
	caption := clampRunes(strings.TrimSpace(r.FormValue("text")), maxMessageRunes)
	preview := r.FormValue("preview") == "1" || r.FormValue("preview") == "true"

	display := rm.ApplyNick(u.UID, wireName(u), user.SanitizeName(r.FormValue("nick")))
	m := rm.AddFile(display, u.UID, name, ctype, data, caption, parseMentions(caption), preview, r.FormValue("replyTo"))
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
	if s.admin.channelBanned(rm.ID) {
		http.Error(w, bannedChannelMsg, http.StatusForbidden)
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
	// A served file is wholly user-controlled. Even with the inline whitelist
	// below, give every download a deny-all, sandboxed CSP so that a payload
	// that does get interpreted as a document (e.g. opened top-level) can never
	// run script or reach this origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")

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
// though the type is supplied by the uploader. SVG is explicitly excluded:
// unlike raster images it is an active document type that can embed script,
// so rendering an uploaded SVG inline at this origin would be stored XSS.
func isInlineMedia(ctype string) bool {
	ctype = strings.ToLower(strings.TrimSpace(ctype))
	if i := strings.IndexByte(ctype, ';'); i >= 0 {
		ctype = strings.TrimSpace(ctype[:i])
	}
	if strings.Contains(ctype, "svg") || strings.Contains(ctype, "xml") {
		return false
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
