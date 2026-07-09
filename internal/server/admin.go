// Admin panel: a server-operator backend, disabled by default, that manages
// site-wide state on top of the per-channel owner/admin moderation model.
//
// The panel is turned on in the configuration (admin.enabled plus a mandatory
// admin.token) and authenticated per request with that token. It can:
//
//   - disable (ban) whole channels and ban users site-wide;
//   - grant per-channel and per-user exemptions from the upload size limit
//     (an override may raise or lower the effective limit);
//   - edit the server's runtime configuration (the same knobs the config file
//     exposes) with immediate effect.
//
// All of this state is persisted through the storage KV interface, so it
// survives restarts on persistent backends. Enforcement stays active even if
// the panel is later disabled; only the management API is gated.
package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/moehoshio/NekoDrop/internal/room"
	"github.com/moehoshio/NekoDrop/internal/storage"
	"github.com/moehoshio/NekoDrop/internal/user"
)

// adminStateKey is the storage KV key holding the serialized admin state.
const adminStateKey = "admin_state"

// runtimeOverrides are the configuration values an administrator may change at
// runtime. A nil field means "no override": the value from the config file /
// environment / flags applies.
type runtimeOverrides struct {
	MaxUploadBytes           *int64 `json:"maxUploadBytes,omitempty"`
	MaxChannelsPerUser       *int   `json:"maxChannelsPerUser,omitempty"`
	MaxMessagesPerChannel    *int   `json:"maxMessagesPerChannel,omitempty"`
	MaxFileBytesPerChannel   *int64 `json:"maxFileBytesPerChannel,omitempty"`
	MaxBytesPerChannel       *int64 `json:"maxBytesPerChannel,omitempty"`
	MaxSubscribersPerChannel *int   `json:"maxSubscribersPerChannel,omitempty"`
	MaxChannels              *int   `json:"maxChannels,omitempty"`
	MaxUsers                 *int   `json:"maxUsers,omitempty"`
}

// adminState is the persisted portion of the admin panel: site-wide bans,
// per-user/per-channel upload exemptions, and runtime config overrides.
// Channels are keyed by their permanent channel ID (not the reusable key), so
// a ban sticks to the channel itself.
type adminState struct {
	BannedUsers      map[string]bool  `json:"bannedUsers,omitempty"`
	BannedChannels   map[string]bool  `json:"bannedChannels,omitempty"`
	UserMaxUpload    map[string]int64 `json:"userMaxUpload,omitempty"`
	ChannelMaxUpload map[string]int64 `json:"channelMaxUpload,omitempty"`
	Config           runtimeOverrides `json:"config"`
}

// adminPanel carries the panel's configuration and guarded state.
type adminPanel struct {
	enabled bool
	token   string
	store   storage.Store

	mu    sync.RWMutex
	state adminState
}

func newAdminPanel(enabled bool, token string, store storage.Store) *adminPanel {
	a := &adminPanel{enabled: enabled && token != "", token: token, store: store}
	a.state = adminState{
		BannedUsers:      map[string]bool{},
		BannedChannels:   map[string]bool{},
		UserMaxUpload:    map[string]int64{},
		ChannelMaxUpload: map[string]int64{},
	}
	if store != nil {
		if raw, ok, err := store.LoadKV(adminStateKey); err == nil && ok {
			var st adminState
			if json.Unmarshal([]byte(raw), &st) == nil {
				if st.BannedUsers == nil {
					st.BannedUsers = map[string]bool{}
				}
				if st.BannedChannels == nil {
					st.BannedChannels = map[string]bool{}
				}
				if st.UserMaxUpload == nil {
					st.UserMaxUpload = map[string]int64{}
				}
				if st.ChannelMaxUpload == nil {
					st.ChannelMaxUpload = map[string]int64{}
				}
				a.state = st
			}
		}
	}
	return a
}

// persistLocked serializes the state to the KV store. The caller holds a.mu
// (read or write); persistence is best-effort like the rest of the store.
func (a *adminPanel) persistLocked() {
	if a.store == nil {
		return
	}
	if b, err := json.Marshal(a.state); err == nil {
		_ = a.store.SaveKV(adminStateKey, string(b))
	}
}

// authorized reports whether the request carries the admin token. Tokens are
// compared as SHA-256 digests so the comparison is constant-time and does not
// leak the token length.
func (a *adminPanel) authorized(r *http.Request) bool {
	got := sha256.Sum256([]byte(r.Header.Get("X-Admin-Token")))
	want := sha256.Sum256([]byte(a.token))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

// --- Enforcement lookups (hot path; read lock only) ---

func (a *adminPanel) userBanned(uid string) bool {
	if uid == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.BannedUsers[uid]
}

func (a *adminPanel) channelBanned(id string) bool {
	if id == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.BannedChannels[id]
}

// uploadLimit resolves the effective per-file upload limit for uid posting in
// channel id: a per-user exemption wins over a per-channel one, which wins over
// the runtime configuration override, which wins over the configured base.
func (a *adminPanel) uploadLimit(base int64, uid, channelID string) int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if uid != "" {
		if v, ok := a.state.UserMaxUpload[uid]; ok {
			return v
		}
	}
	if channelID != "" {
		if v, ok := a.state.ChannelMaxUpload[channelID]; ok {
			return v
		}
	}
	if v := a.state.Config.MaxUploadBytes; v != nil {
		return *v
	}
	return base
}

func (a *adminPanel) overrides() runtimeOverrides {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state.Config
}

// --- HTTP handlers ---

const (
	bannedUserMsg    = "your account has been restricted by an administrator"
	bannedChannelMsg = "this channel has been disabled by an administrator"
)

// requireAdmin gates every admin endpoint: 404 while the panel is disabled (so
// its existence is not revealed) and 401 for a missing or wrong token.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !s.admin.enabled {
		http.NotFound(w, r)
		return false
	}
	if !s.admin.authorized(r) {
		http.Error(w, "invalid admin token", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !s.admin.enabled {
		http.NotFound(w, r)
		return
	}
	s.serveFile(w, r, "admin.html", "text/html; charset=utf-8")
}

// handleAdminOverview serves the dashboard statistics: live counters from the
// hub and registry, resident content sizes, Go process memory, uptime, and —
// on persistent backends — database totals.
func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	a := s.admin
	a.mu.RLock()
	bannedUsers, bannedChannels := len(a.state.BannedUsers), len(a.state.BannedChannels)
	a.mu.RUnlock()

	hubStats := s.hub.Stats()
	userStats := s.users.Stats()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	payload := map[string]any{
		"backend":       s.store.Backend(),
		"uptimeSeconds": int64(time.Since(s.startTime).Seconds()),

		// Live state.
		"channels":       hubStats.Channels,
		"users":          userStats.Users,
		"namedUsers":     userStats.Named,
		"migrationUsers": userStats.Migration,
		"online":         hubStats.Online,
		"connections":    hubStats.Subscribers,
		"bannedUsers":    bannedUsers,
		"bannedChannels": bannedChannels,

		// Resident content.
		"messages":      hubStats.Messages,
		"files":         hubStats.Files,
		"announcements": hubStats.Announcements,
		"textBytes":     hubStats.TextBytes,
		"fileBytes":     hubStats.FileBytes,
		"residentBytes": hubStats.TextBytes + hubStats.FileBytes,

		// Go process.
		"memHeapBytes": mem.HeapAlloc,
		"memSysBytes":  mem.Sys,
		"goroutines":   runtime.NumGoroutine(),
	}
	if st, err := s.store.Stats(); err == nil && st.Persistent {
		payload["store"] = map[string]any{
			"persistent":    true,
			"sizeBytes":     st.SizeBytes,
			"users":         st.Users,
			"channels":      st.Channels,
			"messages":      st.Messages,
			"files":         st.Files,
			"fileBytes":     st.FileBytes,
			"announcements": st.Announcements,
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

// adminChannel is a channel row in the admin roster: its public info plus the
// admin-only standing (site-wide ban, upload exemption).
type adminChannel struct {
	room.Info
	BannedByAdmin  bool   `json:"bannedByAdmin"`
	MaxUploadBytes *int64 `json:"maxUploadBytes,omitempty"`
}

func (s *Server) adminChannelView(info room.Info) adminChannel {
	a := s.admin
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := adminChannel{Info: info, BannedByAdmin: a.state.BannedChannels[info.ID]}
	if v, ok := a.state.ChannelMaxUpload[info.ID]; ok {
		out.MaxUploadBytes = &v
	}
	return out
}

func (s *Server) handleAdminChannels(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	infos := s.hub.AllInfos()
	out := make([]adminChannel, 0, len(infos))
	for _, info := range infos {
		if q != "" && !strings.Contains(strings.ToLower(info.Key), q) &&
			!strings.Contains(strings.ToLower(info.Name), q) &&
			!strings.Contains(strings.ToLower(info.ID), q) {
			continue
		}
		out = append(out, s.adminChannelView(info))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

// adminModerationPatch mutates one target's admin standing. Absent fields are
// untouched; maxUploadBytes accepts a positive byte count or null to clear the
// exemption.
type adminModerationPatch struct {
	Banned         *bool  `json:"banned"`
	MaxUploadBytes *int64 `json:"maxUploadBytes"`
	// clearUpload distinguishes an explicit null from an absent field.
	clearUpload bool
}

func decodeModerationPatch(r *http.Request) (adminModerationPatch, error) {
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		return adminModerationPatch{}, err
	}
	var p adminModerationPatch
	if v, ok := raw["banned"]; ok {
		if err := json.Unmarshal(v, &p.Banned); err != nil {
			return p, err
		}
	}
	if v, ok := raw["maxUploadBytes"]; ok {
		if string(v) == "null" {
			p.clearUpload = true
		} else if err := json.Unmarshal(v, &p.MaxUploadBytes); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (s *Server) handleAdminPatchChannel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	rm, found := s.hub.LookupID(id)
	patch, err := decodeModerationPatch(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if patch.MaxUploadBytes != nil && *patch.MaxUploadBytes <= 0 {
		http.Error(w, "maxUploadBytes must be positive (or null to clear)", http.StatusBadRequest)
		return
	}
	// Setting state requires a live channel; clearing state is always allowed so
	// stale entries for reclaimed channels can be cleaned up.
	setting := (patch.Banned != nil && *patch.Banned) || patch.MaxUploadBytes != nil
	if !found && setting {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	a := s.admin
	a.mu.Lock()
	if patch.Banned != nil {
		if *patch.Banned {
			a.state.BannedChannels[id] = true
		} else {
			delete(a.state.BannedChannels, id)
		}
	}
	if patch.clearUpload {
		delete(a.state.ChannelMaxUpload, id)
	} else if patch.MaxUploadBytes != nil {
		a.state.ChannelMaxUpload[id] = *patch.MaxUploadBytes
	}
	a.persistLocked()
	a.mu.Unlock()

	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, s.adminChannelView(rm.Info()))
}

// adminUser is a user row in the admin roster.
type adminUser struct {
	UID            string `json:"uid"`
	Name           string `json:"name"`
	Named          bool   `json:"named"`
	Migration      bool   `json:"migration"`
	Banned         bool   `json:"banned"`
	MaxUploadBytes *int64 `json:"maxUploadBytes,omitempty"`
}

func (s *Server) adminUserView(u user.User) adminUser {
	a := s.admin
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := adminUser{
		UID:       u.UID,
		Name:      u.Name,
		Named:     u.Named,
		Migration: u.MigrateCode != "",
		Banned:    a.state.BannedUsers[u.UID],
	}
	if v, ok := a.state.UserMaxUpload[u.UID]; ok {
		out.MaxUploadBytes = &v
	}
	return out
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	users := s.users.List(r.URL.Query().Get("q"), 500)
	out := make([]adminUser, 0, len(users))
	for _, u := range users {
		out = append(out, s.adminUserView(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleAdminPatchUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	uid := strings.TrimSpace(r.PathValue("uid"))
	if uid == "" {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	patch, err := decodeModerationPatch(r)
	if err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if patch.MaxUploadBytes != nil && *patch.MaxUploadBytes <= 0 {
		http.Error(w, "maxUploadBytes must be positive (or null to clear)", http.StatusBadRequest)
		return
	}

	a := s.admin
	a.mu.Lock()
	if patch.Banned != nil {
		if *patch.Banned {
			a.state.BannedUsers[uid] = true
		} else {
			delete(a.state.BannedUsers, uid)
		}
	}
	if patch.clearUpload {
		delete(a.state.UserMaxUpload, uid)
	} else if patch.MaxUploadBytes != nil {
		a.state.UserMaxUpload[uid] = *patch.MaxUploadBytes
	}
	a.persistLocked()
	a.mu.Unlock()

	// The user may not be resident in memory (evicted or never seen); report
	// their standing regardless, since bans are keyed by UID alone.
	target := user.User{UID: uid, Name: user.DefaultName}
	if u := s.users.ByUID(uid); u != nil {
		target = *u
	}
	writeJSON(w, http.StatusOK, s.adminUserView(target))
}

// runtimeConfigValues is the JSON shape used for the base and effective views
// of the runtime configuration.
type runtimeConfigValues struct {
	MaxUploadBytes           int64 `json:"maxUploadBytes"`
	MaxChannelsPerUser       int   `json:"maxChannelsPerUser"`
	MaxMessagesPerChannel    int   `json:"maxMessagesPerChannel"`
	MaxFileBytesPerChannel   int64 `json:"maxFileBytesPerChannel"`
	MaxBytesPerChannel       int64 `json:"maxBytesPerChannel"`
	MaxSubscribersPerChannel int   `json:"maxSubscribersPerChannel"`
	MaxChannels              int   `json:"maxChannels"`
	MaxUsers                 int   `json:"maxUsers"`
}

// baseConfigValues returns the configuration as resolved from file/env/flags,
// with unset values replaced by the documented defaults.
func (s *Server) baseConfigValues() runtimeConfigValues {
	l := s.baseLimits
	d := room.DefaultLimits
	v := runtimeConfigValues{
		MaxUploadBytes:           s.baseMaxUpload,
		MaxChannelsPerUser:       s.baseMaxChannelsPerUser,
		MaxMessagesPerChannel:    l.MaxMessagesPerChannel,
		MaxFileBytesPerChannel:   l.MaxFileBytesPerChannel,
		MaxBytesPerChannel:       l.MaxBytesPerChannel,
		MaxSubscribersPerChannel: l.MaxSubscribersPerChannel,
		MaxChannels:              l.MaxChannels,
		MaxUsers:                 s.baseMaxUsers,
	}
	if v.MaxMessagesPerChannel <= 0 {
		v.MaxMessagesPerChannel = d.MaxMessagesPerChannel
	}
	if v.MaxFileBytesPerChannel <= 0 {
		v.MaxFileBytesPerChannel = d.MaxFileBytesPerChannel
	}
	if v.MaxBytesPerChannel <= 0 {
		v.MaxBytesPerChannel = d.MaxBytesPerChannel
	}
	if v.MaxSubscribersPerChannel <= 0 {
		v.MaxSubscribersPerChannel = d.MaxSubscribersPerChannel
	}
	if v.MaxChannels <= 0 {
		v.MaxChannels = d.MaxChannels
	}
	if v.MaxUsers <= 0 {
		v.MaxUsers = user.DefaultMaxUsers
	}
	return v
}

// effectiveConfigValues overlays the runtime overrides onto the base values.
func (s *Server) effectiveConfigValues() runtimeConfigValues {
	v := s.baseConfigValues()
	ov := s.admin.overrides()
	if ov.MaxUploadBytes != nil {
		v.MaxUploadBytes = *ov.MaxUploadBytes
	}
	if ov.MaxChannelsPerUser != nil {
		v.MaxChannelsPerUser = *ov.MaxChannelsPerUser
	}
	if ov.MaxMessagesPerChannel != nil {
		v.MaxMessagesPerChannel = *ov.MaxMessagesPerChannel
	}
	if ov.MaxFileBytesPerChannel != nil {
		v.MaxFileBytesPerChannel = *ov.MaxFileBytesPerChannel
	}
	if ov.MaxBytesPerChannel != nil {
		v.MaxBytesPerChannel = *ov.MaxBytesPerChannel
	}
	if ov.MaxSubscribersPerChannel != nil {
		v.MaxSubscribersPerChannel = *ov.MaxSubscribersPerChannel
	}
	if ov.MaxChannels != nil {
		v.MaxChannels = *ov.MaxChannels
	}
	if ov.MaxUsers != nil {
		v.MaxUsers = *ov.MaxUsers
	}
	return v
}

func (s *Server) writeAdminConfig(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"base":      s.baseConfigValues(),
		"overrides": s.admin.overrides(),
		"effective": s.effectiveConfigValues(),
	})
}

func (s *Server) handleAdminGetConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.writeAdminConfig(w)
}

// handleAdminPatchConfig edits the runtime configuration: each supplied field
// becomes an override (applied immediately), and an explicit null clears the
// override, reverting to the config-file value. Absent fields are untouched.
func (s *Server) handleAdminPatchConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var raw map[string]json.RawMessage
	if err := decodeJSON(r, &raw); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	a := s.admin
	a.mu.Lock()
	cfg := a.state.Config
	err := func() error {
		if err := patchInt64(raw, "maxUploadBytes", &cfg.MaxUploadBytes, true); err != nil {
			return err
		}
		// 0 = unlimited for maxChannelsPerUser, so zero is a valid override.
		if err := patchInt(raw, "maxChannelsPerUser", &cfg.MaxChannelsPerUser, false); err != nil {
			return err
		}
		if err := patchInt(raw, "maxMessagesPerChannel", &cfg.MaxMessagesPerChannel, true); err != nil {
			return err
		}
		if err := patchInt64(raw, "maxFileBytesPerChannel", &cfg.MaxFileBytesPerChannel, true); err != nil {
			return err
		}
		if err := patchInt64(raw, "maxBytesPerChannel", &cfg.MaxBytesPerChannel, true); err != nil {
			return err
		}
		if err := patchInt(raw, "maxSubscribersPerChannel", &cfg.MaxSubscribersPerChannel, true); err != nil {
			return err
		}
		if err := patchInt(raw, "maxChannels", &cfg.MaxChannels, true); err != nil {
			return err
		}
		return patchInt(raw, "maxUsers", &cfg.MaxUsers, true)
	}()
	if err != nil {
		a.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.state.Config = cfg
	a.persistLocked()
	a.mu.Unlock()

	s.applyRuntimeConfig()
	s.writeAdminConfig(w)
}

// applyRuntimeConfig pushes the current effective configuration into the live
// hub and registry. The per-file upload limit is resolved per request and
// needs no push.
func (s *Server) applyRuntimeConfig() {
	ov := s.admin.overrides()

	limits := s.baseLimits
	if ov.MaxMessagesPerChannel != nil {
		limits.MaxMessagesPerChannel = *ov.MaxMessagesPerChannel
	}
	if ov.MaxFileBytesPerChannel != nil {
		limits.MaxFileBytesPerChannel = *ov.MaxFileBytesPerChannel
	}
	if ov.MaxBytesPerChannel != nil {
		limits.MaxBytesPerChannel = *ov.MaxBytesPerChannel
	}
	if ov.MaxSubscribersPerChannel != nil {
		limits.MaxSubscribersPerChannel = *ov.MaxSubscribersPerChannel
	}
	if ov.MaxChannels != nil {
		limits.MaxChannels = *ov.MaxChannels
	}
	s.hub.ApplyLimits(limits)

	maxPer := s.baseMaxChannelsPerUser
	if ov.MaxChannelsPerUser != nil {
		maxPer = *ov.MaxChannelsPerUser
	}
	s.hub.SetMaxPerUID(maxPer)

	maxUsers := s.baseMaxUsers
	if ov.MaxUsers != nil {
		maxUsers = *ov.MaxUsers
	}
	s.users.SetMaxUsers(maxUsers)
}

// patchInt applies raw[key] to *dst: a number sets the override, an explicit
// null clears it, an absent key leaves it untouched. requirePositive rejects
// values < 1 (limits where zero would silently mean "default").
func patchInt(raw map[string]json.RawMessage, key string, dst **int, requirePositive bool) error {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	if string(v) == "null" {
		*dst = nil
		return nil
	}
	var n int
	if err := json.Unmarshal(v, &n); err != nil {
		return errBadField(key)
	}
	if n < 0 || (requirePositive && n < 1) {
		return errBadField(key)
	}
	*dst = &n
	return nil
}

func patchInt64(raw map[string]json.RawMessage, key string, dst **int64, requirePositive bool) error {
	v, ok := raw[key]
	if !ok {
		return nil
	}
	if string(v) == "null" {
		*dst = nil
		return nil
	}
	var n int64
	if err := json.Unmarshal(v, &n); err != nil {
		return errBadField(key)
	}
	if n < 0 || (requirePositive && n < 1) {
		return errBadField(key)
	}
	*dst = &n
	return nil
}

type errBadField string

func (e errBadField) Error() string { return "invalid value for " + string(e) }
