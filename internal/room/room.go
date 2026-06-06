// Package room implements the in-memory channel model used by NekoDrop. A
// channel (historically a "room") is identified by a key derived from a URL
// path or join code, and additionally by a permanent, unique channel ID.
//
// Channels carry metadata (name, description, visibility), an ownership and
// moderation model (owner, admins, members, banned and muted users), a history
// of messages (text and file metadata), uploaded file payloads, pinned
// announcements, and the set of subscribers that receive real-time events
// (new messages, presence changes, announcements and channel updates).
//
// All exported methods on Room and Hub are safe for concurrent use.
package room

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// MessageKind enumerates the kinds of messages a channel can hold.
type MessageKind string

const (
	// KindText is a plain text chat message.
	KindText MessageKind = "text"
	// KindFile is a shared file made available for download.
	KindFile MessageKind = "file"
)

// Visibility controls who may read a channel's contents.
type Visibility string

const (
	// Public channels can be read by anyone; joining is only required to speak.
	Public Visibility = "public"
	// Private channels can only be read by members.
	Private Visibility = "private"
)

// Message is a single entry in a channel's history. It is safe to serialize to
// JSON and send to clients; file payloads are stored separately and referenced
// through FileID.
type Message struct {
	ID        string      `json:"id"`
	Kind      MessageKind `json:"kind"`
	Sender    string      `json:"sender"`
	SenderUID string      `json:"senderUid"`
	Text      string      `json:"text,omitempty"`
	Mentions  []string    `json:"mentions,omitempty"`
	Preview   bool        `json:"preview,omitempty"`
	FileID    string      `json:"fileId,omitempty"`
	FileName  string      `json:"fileName,omitempty"`
	FileSize  int64       `json:"fileSize,omitempty"`
	FileType  string      `json:"fileType,omitempty"`
	Time      time.Time   `json:"time"`
}

// Announcement is a pinned notice posted by a channel owner or admin.
type Announcement struct {
	ID         string    `json:"id"`
	AuthorUID  string    `json:"authorUid"`
	AuthorName string    `json:"authorName"`
	Text       string    `json:"text"`
	Time       time.Time `json:"time"`
}

// File is an uploaded payload stored in memory and referenced by messages.
type File struct {
	ID          string
	Name        string
	ContentType string
	Data        []byte
}

// Size returns the byte length of the stored file.
func (f *File) Size() int64 { return int64(len(f.Data)) }

// EventKind enumerates the kinds of real-time events pushed to subscribers.
type EventKind string

const (
	// EventMessage carries a new chat message.
	EventMessage EventKind = "message"
	// EventPresence reports a change in the online member count.
	EventPresence EventKind = "presence"
	// EventAnnouncement carries a newly posted announcement.
	EventAnnouncement EventKind = "announcement"
	// EventChannel reports a change to channel metadata or settings.
	EventChannel EventKind = "channel"
	// EventDissolved signals that the channel has been dissolved.
	EventDissolved EventKind = "dissolved"
)

// Event is a single real-time update delivered over the subscription channel.
// Exactly one of the payload fields is populated, selected by Type.
type Event struct {
	Type         EventKind     `json:"type"`
	Message      *Message      `json:"message,omitempty"`
	Online       int           `json:"online,omitempty"`
	Announcement *Announcement `json:"announcement,omitempty"`
	Channel      *Info         `json:"channel,omitempty"`
}

// Settings is the mutable, owner-controlled configuration of a channel.
type Settings struct {
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Visibility      Visibility `json:"visibility"`
	ListPublic      bool       `json:"listPublic"`
	AllowJoin       bool       `json:"allowJoin"`
	RequireApproval bool       `json:"requireApproval"`
	AllowSpeak      bool       `json:"allowSpeak"`
}

// Info is a JSON-friendly snapshot of a channel's public-facing metadata.
type Info struct {
	ID              string     `json:"id"`
	Key             string     `json:"key"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	Visibility      Visibility `json:"visibility"`
	OwnerUID        string     `json:"ownerUid"`
	ListPublic      bool       `json:"listPublic"`
	AllowJoin       bool       `json:"allowJoin"`
	RequireApproval bool       `json:"requireApproval"`
	AllowSpeak      bool       `json:"allowSpeak"`
	Online          int        `json:"online"`
	Members         int        `json:"members"`
	Owned           bool       `json:"owned"`
	Dissolved       bool       `json:"dissolved"`
}

var (
	// ErrFileNotFound is returned when a requested file does not exist.
	ErrFileNotFound = errors.New("file not found")
	// ErrChannelExists is returned when creating a channel whose key is taken.
	ErrChannelExists = errors.New("channel key already in use")
	// ErrChannelNotFound is returned when a channel key/ID is unknown.
	ErrChannelNotFound = errors.New("channel not found")
	// ErrLimitReached is returned when an owner hits their channel quota.
	ErrLimitReached = errors.New("channel creation limit reached")
)

// Room is a single channel. All exported methods are safe for concurrent use.
type Room struct {
	Key string
	ID  string

	mu       sync.RWMutex
	settings Settings
	ownerUID string

	admins  map[string]bool
	members map[string]bool
	banned  map[string]bool
	muted   map[string]bool
	pending map[string]string // uid -> display name, awaiting approval

	messages      []Message
	files         map[string]*File
	announcements []Announcement

	online      map[string]int // uid -> active connection count
	subscribers map[chan Event]struct{}

	dissolved bool
}

func newRoom(key, id string) *Room {
	return &Room{
		Key:         key,
		ID:          id,
		settings:    Settings{Name: key, Visibility: Public, AllowJoin: true, AllowSpeak: true},
		admins:      make(map[string]bool),
		members:     make(map[string]bool),
		banned:      make(map[string]bool),
		muted:       make(map[string]bool),
		pending:     make(map[string]string),
		files:       make(map[string]*File),
		online:      make(map[string]int),
		subscribers: make(map[chan Event]struct{}),
	}
}

// --- Permission helpers (caller must hold at least a read lock) ---

func (r *Room) ownerless() bool { return r.ownerUID == "" }

func (r *Room) isOwner(uid string) bool { return uid != "" && uid == r.ownerUID }

func (r *Room) isAdmin(uid string) bool { return r.isOwner(uid) || (uid != "" && r.admins[uid]) }

func (r *Room) isMember(uid string) bool {
	return r.isOwner(uid) || (uid != "" && r.members[uid])
}

// canRead reports whether uid may view the channel's content.
func (r *Room) canRead(uid string) bool {
	if r.banned[uid] && r.settings.Visibility == Private {
		return false
	}
	if r.settings.Visibility == Public || r.ownerless() {
		return true
	}
	return r.isMember(uid) || r.isAdmin(uid)
}

// canSpeak reports whether uid may post messages or files.
func (r *Room) canSpeak(uid string) bool {
	if r.banned[uid] || r.muted[uid] {
		return false
	}
	if r.ownerless() {
		return true // legacy open rooms: anyone present may speak
	}
	if !r.isMember(uid) {
		return false
	}
	return r.settings.AllowSpeak || r.isAdmin(uid)
}

// CanRead is the exported, lock-taking form of canRead.
func (r *Room) CanRead(uid string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.canRead(uid)
}

// CanSpeak is the exported, lock-taking form of canSpeak.
func (r *Room) CanSpeak(uid string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.canSpeak(uid)
}

// IsAdmin reports whether uid owns or administers the channel.
func (r *Room) IsAdmin(uid string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isAdmin(uid)
}

// IsOwner reports whether uid is the channel owner.
func (r *Room) IsOwner(uid string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isOwner(uid)
}

// Role describes a viewer's relationship to a channel, for the client UI.
type Role struct {
	UID      string `json:"uid"`
	Member   bool   `json:"member"`
	Admin    bool   `json:"admin"`
	Owner    bool   `json:"owner"`
	Banned   bool   `json:"banned"`
	Muted    bool   `json:"muted"`
	Pending  bool   `json:"pending"`
	CanRead  bool   `json:"canRead"`
	CanSpeak bool   `json:"canSpeak"`
}

// RoleOf returns the role of uid within the channel.
func (r *Room) RoleOf(uid string) Role {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, pending := r.pending[uid]
	return Role{
		UID:      uid,
		Member:   r.isMember(uid),
		Admin:    r.isAdmin(uid),
		Owner:    r.isOwner(uid),
		Banned:   r.banned[uid],
		Muted:    r.muted[uid],
		Pending:  pending,
		CanRead:  r.canRead(uid),
		CanSpeak: r.canSpeak(uid),
	}
}

// --- Subscription & event fan-out ---

// Subscribe registers uid as present and returns a snapshot of the existing
// message history, the current announcements, a channel of live events, and an
// unsubscribe function that must be called when finished.
func (r *Room) Subscribe(uid string) (history []Message, announcements []Announcement, ch <-chan Event, cancel func()) {
	r.mu.Lock()

	history = make([]Message, len(r.messages))
	copy(history, r.messages)
	announcements = make([]Announcement, len(r.announcements))
	copy(announcements, r.announcements)

	c := make(chan Event, 64)
	r.subscribers[c] = struct{}{}
	if uid != "" {
		r.online[uid]++
	}
	online := len(r.online)
	r.mu.Unlock()

	r.broadcast(Event{Type: EventPresence, Online: online})

	cancel = func() {
		r.mu.Lock()
		if _, ok := r.subscribers[c]; ok {
			delete(r.subscribers, c)
			close(c)
		}
		if uid != "" && r.online[uid] > 0 {
			r.online[uid]--
			if r.online[uid] == 0 {
				delete(r.online, uid)
			}
		}
		online := len(r.online)
		r.mu.Unlock()
		r.broadcast(Event{Type: EventPresence, Online: online})
	}
	return history, announcements, c, cancel
}

// broadcast fans an event out to subscribers. Slow subscribers that cannot keep
// up are skipped for that event rather than blocking the publisher.
func (r *Room) broadcast(e Event) {
	r.mu.RLock()
	subs := make([]chan Event, 0, len(r.subscribers))
	for c := range r.subscribers {
		subs = append(subs, c)
	}
	r.mu.RUnlock()

	for _, c := range subs {
		select {
		case c <- e:
		default:
		}
	}
}

// OnlineCount returns the number of distinct online members.
func (r *Room) OnlineCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.online)
}

// Subscribers returns the current number of active subscriber connections.
func (r *Room) Subscribers() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.subscribers)
}

// --- Messages & files ---

// AddText records a text message and broadcasts it.
func (r *Room) AddText(sender, senderUID, text string, mentions []string, preview bool) Message {
	m := Message{
		ID:        newID(12),
		Kind:      KindText,
		Sender:    sender,
		SenderUID: senderUID,
		Text:      text,
		Mentions:  mentions,
		Preview:   preview,
		Time:      time.Now().UTC(),
	}
	r.mu.Lock()
	r.messages = append(r.messages, m)
	r.mu.Unlock()
	r.broadcast(Event{Type: EventMessage, Message: &m})
	return m
}

// AddFile stores a file payload, records a file message referencing it, and
// broadcasts the message.
func (r *Room) AddFile(sender, senderUID, name, contentType string, data []byte) Message {
	f := &File{ID: newID(12), Name: name, ContentType: contentType, Data: data}
	m := Message{
		ID:        newID(12),
		Kind:      KindFile,
		Sender:    sender,
		SenderUID: senderUID,
		FileID:    f.ID,
		FileName:  name,
		FileSize:  f.Size(),
		FileType:  contentType,
		Time:      time.Now().UTC(),
	}
	r.mu.Lock()
	r.files[f.ID] = f
	r.messages = append(r.messages, m)
	r.mu.Unlock()
	r.broadcast(Event{Type: EventMessage, Message: &m})
	return m
}

// File returns the stored file with the given id, or ErrFileNotFound.
func (r *Room) File(id string) (*File, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.files[id]
	if !ok {
		return nil, ErrFileNotFound
	}
	return f, nil
}

// --- Announcements ---

// Announcements returns a copy of the channel's announcements.
func (r *Room) Announcements() []Announcement {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Announcement, len(r.announcements))
	copy(out, r.announcements)
	return out
}

// AddAnnouncement appends an announcement and broadcasts it.
func (r *Room) AddAnnouncement(authorUID, authorName, text string) Announcement {
	a := Announcement{
		ID:         newID(8),
		AuthorUID:  authorUID,
		AuthorName: authorName,
		Text:       text,
		Time:       time.Now().UTC(),
	}
	r.mu.Lock()
	r.announcements = append(r.announcements, a)
	r.mu.Unlock()
	r.broadcast(Event{Type: EventAnnouncement, Announcement: &a})
	return a
}

// --- Membership & moderation ---

// Join adds uid as a member. When the channel requires approval, the request is
// instead queued and pending is returned true. Banned users cannot join, and a
// channel that disallows joining rejects new members.
func (r *Room) Join(uid, name string) (pending bool, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dissolved {
		return false, ErrChannelNotFound
	}
	if r.banned[uid] {
		return false, errors.New("you are banned from this channel")
	}
	if r.isMember(uid) {
		return false, nil
	}
	if !r.settings.AllowJoin {
		return false, errors.New("this channel is not accepting new members")
	}
	if r.settings.RequireApproval {
		r.pending[uid] = name
		return true, nil
	}
	r.members[uid] = true
	return false, nil
}

// Leave removes uid's membership and admin role. Owners cannot leave; they must
// dissolve the channel instead.
func (r *Room) Leave(uid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.isOwner(uid) {
		return
	}
	delete(r.members, uid)
	delete(r.admins, uid)
	delete(r.pending, uid)
}

// Moderate applies a moderation action issued by actorUID against targetUID.
// Supported actions: promote, demote, ban, unban, mute, unmute, approve,
// reject, kick. The owner can never be the target.
func (r *Room) Moderate(actorUID, action, targetUID string) error {
	r.mu.Lock()
	changed := false
	err := func() error {
		if !r.isAdmin(actorUID) {
			return errors.New("not authorized")
		}
		if targetUID == "" {
			return errors.New("invalid target")
		}
		if r.isOwner(targetUID) {
			return errors.New("the owner cannot be moderated")
		}
		// Only the owner may add or remove admins.
		switch action {
		case "promote", "demote":
			if !r.isOwner(actorUID) {
				return errors.New("only the owner can manage admins")
			}
		}
		switch action {
		case "promote":
			r.members[targetUID] = true
			r.admins[targetUID] = true
		case "demote":
			delete(r.admins, targetUID)
		case "ban":
			r.banned[targetUID] = true
			delete(r.members, targetUID)
			delete(r.admins, targetUID)
			delete(r.pending, targetUID)
		case "unban":
			delete(r.banned, targetUID)
		case "mute":
			r.muted[targetUID] = true
		case "unmute":
			delete(r.muted, targetUID)
		case "kick":
			delete(r.members, targetUID)
			delete(r.admins, targetUID)
		case "approve":
			if _, ok := r.pending[targetUID]; !ok {
				return errors.New("no such pending request")
			}
			delete(r.pending, targetUID)
			r.members[targetUID] = true
		case "reject":
			delete(r.pending, targetUID)
		default:
			return errors.New("unknown action")
		}
		changed = true
		return nil
	}()
	info := r.infoLocked()
	r.mu.Unlock()
	if err == nil && changed {
		r.broadcast(Event{Type: EventChannel, Channel: &info})
	}
	return err
}

// PendingMember is a queued join request awaiting approval.
type PendingMember struct {
	UID  string `json:"uid"`
	Name string `json:"name"`
}

// Pending returns the queued join requests (admin view).
func (r *Room) Pending() []PendingMember {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]PendingMember, 0, len(r.pending))
	for uid, name := range r.pending {
		out = append(out, PendingMember{UID: uid, Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out
}

// --- Settings & metadata ---

// Update applies owner/admin-supplied settings. The actor must be an admin.
func (r *Room) Update(actorUID string, s Settings) (Info, error) {
	r.mu.Lock()
	if !r.isAdmin(actorUID) {
		r.mu.Unlock()
		return Info{}, errors.New("not authorized")
	}
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		s.Name = r.Key
	}
	if len([]rune(s.Name)) > 64 {
		s.Name = string([]rune(s.Name)[:64])
	}
	if len([]rune(s.Description)) > 280 {
		s.Description = string([]rune(s.Description)[:280])
	}
	if s.Visibility != Public && s.Visibility != Private {
		s.Visibility = r.settings.Visibility
	}
	r.settings = s
	info := r.infoLocked()
	r.mu.Unlock()
	r.broadcast(Event{Type: EventChannel, Channel: &info})
	return info, nil
}

// Info returns a snapshot of the channel metadata.
func (r *Room) Info() Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.infoLocked()
}

func (r *Room) infoLocked() Info {
	return Info{
		ID:              r.ID,
		Key:             r.Key,
		Name:            r.settings.Name,
		Description:     r.settings.Description,
		Visibility:      r.settings.Visibility,
		OwnerUID:        r.ownerUID,
		ListPublic:      r.settings.ListPublic,
		AllowJoin:       r.settings.AllowJoin,
		RequireApproval: r.settings.RequireApproval,
		AllowSpeak:      r.settings.AllowSpeak,
		Online:          len(r.online),
		Members:         len(r.members),
		Owned:           !r.ownerless(),
		Dissolved:       r.dissolved,
	}
}

// --- Hub ---

// Hub manages the set of live channels, indexed by key and by permanent ID, and
// enforces per-owner creation quotas.
type Hub struct {
	mu        sync.Mutex
	byKey     map[string]*Room
	byID      map[string]*Room
	usedIDs   map[string]bool // every ID ever issued; never reused
	ownerNum  map[string]int  // active channels per owner UID
	maxPerUID int
}

// NewHub returns an empty Hub. maxPerUID is the number of channels a single
// owner may have active at once (<= 0 means unlimited).
func NewHub(maxPerUID int) *Hub {
	return &Hub{
		byKey:     make(map[string]*Room),
		byID:      make(map[string]*Room),
		usedIDs:   make(map[string]bool),
		ownerNum:  make(map[string]int),
		maxPerUID: maxPerUID,
	}
}

// Room returns the channel for key, creating an ownerless, open, public channel
// on demand. This preserves NekoDrop's quick, account-free ad-hoc rooms.
func (h *Hub) Room(key string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.byKey[key]; ok {
		return r
	}
	return h.createLocked(key)
}

// Lookup returns an existing channel by key without creating one.
func (h *Hub) Lookup(key string) (*Room, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.byKey[key]
	return r, ok
}

// Create makes a new owned channel with the given key and settings. It fails if
// the key is taken or the owner is over quota.
func (h *Hub) Create(key, ownerUID string, s Settings) (*Room, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.byKey[key]; ok {
		// An ownerless ad-hoc room may be claimed by the first creator.
		if !existing.ownerless() {
			return nil, ErrChannelExists
		}
		return nil, ErrChannelExists
	}
	if h.maxPerUID > 0 && ownerUID != "" && h.ownerNum[ownerUID] >= h.maxPerUID {
		return nil, ErrLimitReached
	}
	r := h.createLocked(key)
	r.ownerUID = ownerUID
	if s.Name == "" {
		s.Name = key
	}
	if s.Visibility != Public && s.Visibility != Private {
		s.Visibility = Public
	}
	r.settings = s
	if ownerUID != "" {
		h.ownerNum[ownerUID]++
	}
	return r, nil
}

func (h *Hub) createLocked(key string) *Room {
	id := h.newChannelID()
	r := newRoom(key, id)
	h.byKey[key] = r
	h.byID[id] = r
	return r
}

// Dissolve removes the channel, releasing its key for reuse. The channel ID is
// retired permanently and never reissued. Only the owner may dissolve.
func (h *Hub) Dissolve(key, actorUID string) error {
	h.mu.Lock()
	r, ok := h.byKey[key]
	if !ok {
		h.mu.Unlock()
		return ErrChannelNotFound
	}
	if !r.IsOwner(actorUID) {
		h.mu.Unlock()
		return errors.New("only the owner can dissolve the channel")
	}
	delete(h.byKey, key)
	delete(h.byID, r.ID) // the ID stays in usedIDs and is never reissued
	if r.ownerUID != "" && h.ownerNum[r.ownerUID] > 0 {
		h.ownerNum[r.ownerUID]--
	}
	h.mu.Unlock()

	r.mu.Lock()
	r.dissolved = true
	info := r.infoLocked()
	subs := make([]chan Event, 0, len(r.subscribers))
	for c := range r.subscribers {
		subs = append(subs, c)
	}
	r.mu.Unlock()

	// Notify and disconnect everyone currently connected.
	for _, c := range subs {
		select {
		case c <- Event{Type: EventDissolved, Channel: &info}:
		default:
		}
	}
	return nil
}

// PublicList returns the channels that have opted into the public directory,
// sorted by online count then name.
func (h *Hub) PublicList() []Info {
	h.mu.Lock()
	rooms := make([]*Room, 0, len(h.byKey))
	for _, r := range h.byKey {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	out := make([]Info, 0, len(rooms))
	for _, r := range rooms {
		info := r.Info()
		if info.ListPublic && !info.Dissolved {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online > out[j].Online
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// CreatedBy returns how many active channels ownerUID currently owns.
func (h *Hub) CreatedBy(ownerUID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ownerNum[ownerUID]
}

// MaxPerUID returns the configured per-owner channel quota (0 = unlimited).
func (h *Hub) MaxPerUID() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.maxPerUID
}

// Len returns the number of live channels.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.byKey)
}

// newChannelID returns a short, unique, never-before-used channel ID. The
// caller must hold h.mu.
func (h *Hub) newChannelID() string {
	for {
		id := newID(3) // 6 hex chars
		if !h.usedIDs[id] {
			h.usedIDs[id] = true
			return id
		}
	}
}

// newID returns a short random hex identifier of n bytes (2n hex chars).
func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))[:n*2]
	}
	return hex.EncodeToString(b)
}
