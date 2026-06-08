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

	"github.com/moehoshio/NekoDrop/internal/storage"
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

// Limits bound NekoDrop's in-memory footprint so that untrusted, unauthenticated
// traffic cannot exhaust server memory (a denial-of-service vector that is most
// acute for the default in-memory backend, where nothing is offloaded to disk).
//
// Every limit is finite: a non-positive value is replaced by the corresponding
// DefaultLimits value rather than meaning "unlimited", so misconfiguration fails
// safe.
type Limits struct {
	// MaxMessagesPerChannel is the most recent messages kept in memory per
	// channel. Older messages are evicted (and their file payloads freed).
	MaxMessagesPerChannel int
	// MaxFileBytesPerChannel caps the total bytes of uploaded file payloads
	// held in memory for a single channel.
	MaxFileBytesPerChannel int64
	// MaxSubscribersPerChannel caps concurrent live (SSE) connections to one
	// channel.
	MaxSubscribersPerChannel int
	// MaxChannels caps the number of simultaneously live channels; idle,
	// ownerless channels are reclaimed to make room.
	MaxChannels int
}

// maxAnnouncements bounds how many announcements are kept resident per channel.
const maxAnnouncements = 200

// DefaultLimits are the conservative defaults applied when a limit is unset.
var DefaultLimits = Limits{
	MaxMessagesPerChannel:    1000,
	MaxFileBytesPerChannel:   128 << 20, // 128 MiB
	MaxSubscribersPerChannel: 512,
	MaxChannels:              10000,
}

func (l Limits) withDefaults() Limits {
	out := l
	if out.MaxMessagesPerChannel <= 0 {
		out.MaxMessagesPerChannel = DefaultLimits.MaxMessagesPerChannel
	}
	if out.MaxFileBytesPerChannel <= 0 {
		out.MaxFileBytesPerChannel = DefaultLimits.MaxFileBytesPerChannel
	}
	if out.MaxSubscribersPerChannel <= 0 {
		out.MaxSubscribersPerChannel = DefaultLimits.MaxSubscribersPerChannel
	}
	if out.MaxChannels <= 0 {
		out.MaxChannels = DefaultLimits.MaxChannels
	}
	return out
}

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
	names   map[string]string // uid -> last seen display name (roster)

	messages      []Message
	files         map[string]*File
	announcements []Announcement
	fileBytes     int64 // total bytes of file payloads currently in memory

	online      map[string]int // uid -> active connection count
	subscribers map[chan Event]struct{}

	// Per-channel memory bounds.
	maxMessages    int
	maxFileBytes   int64
	maxSubscribers int

	lastActive time.Time

	store     storage.Store
	dissolved bool
}

func newRoom(key, id string) *Room {
	return &Room{
		Key:            key,
		ID:             id,
		settings:       Settings{Name: key, Visibility: Public, AllowJoin: true, AllowSpeak: true},
		admins:         make(map[string]bool),
		members:        make(map[string]bool),
		banned:         make(map[string]bool),
		muted:          make(map[string]bool),
		pending:        make(map[string]string),
		names:          make(map[string]string),
		files:          make(map[string]*File),
		online:         make(map[string]int),
		subscribers:    make(map[chan Event]struct{}),
		maxMessages:    DefaultLimits.MaxMessagesPerChannel,
		maxFileBytes:   DefaultLimits.MaxFileBytesPerChannel,
		maxSubscribers: DefaultLimits.MaxSubscribersPerChannel,
		lastActive:     time.Now(),
		store:          storage.NewMemory(),
	}
}

// applyLimits sets the per-channel bounds. Called right after construction,
// before the room is shared.
func (r *Room) applyLimits(l Limits) {
	r.maxMessages = l.MaxMessagesPerChannel
	r.maxFileBytes = l.MaxFileBytesPerChannel
	r.maxSubscribers = l.MaxSubscribersPerChannel
}

// trimLocked enforces the per-channel memory bounds, evicting the oldest
// messages (and freeing any file payloads they own) until both the file-byte
// budget and the message-count cap are satisfied. The caller holds r.mu.
func (r *Room) trimLocked() {
	for r.maxFileBytes > 0 && r.fileBytes > r.maxFileBytes && len(r.messages) > 1 {
		r.evictOldestLocked()
	}
	if r.maxMessages > 0 && len(r.messages) > r.maxMessages {
		drop := len(r.messages) - r.maxMessages
		for i := 0; i < drop; i++ {
			r.evictOldestLocked()
		}
	}
}

// evictOldestLocked removes the oldest message, freeing its file payload if it
// owned one. The caller holds r.mu.
func (r *Room) evictOldestLocked() {
	if len(r.messages) == 0 {
		return
	}
	old := r.messages[0]
	// Advance the slice header; the backing array is reclaimed on the next
	// append-triggered reallocation, bounding memory to ~2x the cap.
	r.messages = r.messages[1:]
	if old.Kind == KindFile && old.FileID != "" {
		if f, ok := r.files[old.FileID]; ok {
			r.fileBytes -= int64(len(f.Data))
			delete(r.files, old.FileID)
		}
	}
}

// rememberName records uid's most recent display name for the moderation
// roster. The caller must hold the write lock.
func (r *Room) rememberName(uid, name string) {
	if uid == "" {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	r.names[uid] = name
}

// hasStandingLocked reports whether uid has any persistent standing in the
// channel (owner, admin, member, muted, banned or a pending request). Only such
// users appear in the moderation roster, so only their names are worth keeping.
// The caller holds at least a read lock.
func (r *Room) hasStandingLocked(uid string) bool {
	if uid == "" {
		return false
	}
	if r.isOwner(uid) || r.admins[uid] || r.members[uid] || r.muted[uid] || r.banned[uid] {
		return true
	}
	_, pending := r.pending[uid]
	return pending
}

// rememberSenderName retains a sender's display name only when they have a
// standing in the channel. Transient public speakers are not tracked, so a
// flood of fresh, cookieless identities posting to one channel cannot grow the
// roster map without bound. The caller holds the write lock.
func (r *Room) rememberSenderName(uid, name string) {
	if r.hasStandingLocked(uid) {
		r.rememberName(uid, name)
	}
}

// record builds the persistable snapshot of this channel. The caller must hold
// at least a read lock.
func (r *Room) record() storage.Channel {
	c := storage.Channel{
		ID:              r.ID,
		Key:             r.Key,
		OwnerUID:        r.ownerUID,
		Name:            r.settings.Name,
		Description:     r.settings.Description,
		Visibility:      string(r.settings.Visibility),
		ListPublic:      r.settings.ListPublic,
		AllowJoin:       r.settings.AllowJoin,
		RequireApproval: r.settings.RequireApproval,
		AllowSpeak:      r.settings.AllowSpeak,
		Names:           cloneMap(r.names),
		Pending:         cloneMap(r.pending),
	}
	c.Admins = keysOf(r.admins)
	c.Members = keysOf(r.members)
	c.Banned = keysOf(r.banned)
	c.Muted = keysOf(r.muted)
	return c
}

// persist writes the channel snapshot to the store. The caller must NOT hold
// the lock (persistence is best-effort and must not block the hot path).
func (r *Room) persist(c storage.Channel) {
	if r.store == nil {
		return
	}
	_ = r.store.SaveChannel(c)
}

func keysOf(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func cloneMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
	r.lastActive = time.Now()
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

// SubscriberLimitReached reports whether the channel is already at its cap of
// concurrent live connections. It is an approximate, lock-free-of-ordering
// guard used to shed load before establishing a new stream.
func (r *Room) SubscriberLimitReached() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.maxSubscribers > 0 && len(r.subscribers) >= r.maxSubscribers
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
	r.rememberSenderName(senderUID, sender)
	r.lastActive = time.Now()
	r.trimLocked()
	r.mu.Unlock()
	if r.store != nil {
		_ = r.store.AppendMessage(r.messageRecord(m))
	}
	r.broadcast(Event{Type: EventMessage, Message: &m})
	return m
}

// AddFile stores a file payload, records a file message referencing it, and
// broadcasts the message. An optional caption (text) may accompany the file so a
// file and its description are delivered as a single message.
func (r *Room) AddFile(sender, senderUID, name, contentType string, data []byte, text string, mentions []string, preview bool) Message {
	f := &File{ID: newID(12), Name: name, ContentType: contentType, Data: data}
	m := Message{
		ID:        newID(12),
		Kind:      KindFile,
		Sender:    sender,
		SenderUID: senderUID,
		Text:      text,
		Mentions:  mentions,
		Preview:   preview,
		FileID:    f.ID,
		FileName:  name,
		FileSize:  f.Size(),
		FileType:  contentType,
		Time:      time.Now().UTC(),
	}
	r.mu.Lock()
	r.files[f.ID] = f
	r.fileBytes += f.Size()
	r.messages = append(r.messages, m)
	r.rememberSenderName(senderUID, sender)
	r.lastActive = time.Now()
	r.trimLocked()
	r.mu.Unlock()
	if r.store != nil {
		_ = r.store.SaveFile(storage.File{ID: f.ID, ChannelID: r.ID, Name: f.Name, ContentType: f.ContentType, Data: f.Data})
		_ = r.store.AppendMessage(r.messageRecord(m))
	}
	r.broadcast(Event{Type: EventMessage, Message: &m})
	return m
}

// messageRecord converts an in-memory message into its persistable form.
func (r *Room) messageRecord(m Message) storage.Message {
	return storage.Message{
		ID:        m.ID,
		ChannelID: r.ID,
		Kind:      string(m.Kind),
		Sender:    m.Sender,
		SenderUID: m.SenderUID,
		Text:      m.Text,
		Mentions:  m.Mentions,
		Preview:   m.Preview,
		FileID:    m.FileID,
		FileName:  m.FileName,
		FileSize:  m.FileSize,
		FileType:  m.FileType,
		Time:      m.Time,
	}
}

// File returns the stored file with the given id, or ErrFileNotFound. File
// payloads are kept in memory; when a channel has been restored from a durable
// store its blobs are loaded lazily on first download and then cached.
func (r *Room) File(id string) (*File, error) {
	r.mu.RLock()
	f, ok := r.files[id]
	r.mu.RUnlock()
	if ok {
		return f, nil
	}
	if r.store != nil {
		if sf, found, err := r.store.LoadFile(id); err == nil && found {
			f = &File{ID: sf.ID, Name: sf.Name, ContentType: sf.ContentType, Data: sf.Data}
			// Cache the payload only while it keeps us within the file-byte
			// budget; beyond that, serve it straight from the store so a flood
			// of downloads of old files cannot grow memory without bound.
			r.mu.Lock()
			if r.maxFileBytes <= 0 || r.fileBytes+f.Size() <= r.maxFileBytes {
				r.files[id] = f
				r.fileBytes += f.Size()
			}
			r.mu.Unlock()
			return f, nil
		}
	}
	return nil, ErrFileNotFound
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
	// Keep only the most recent announcements resident; the durable store (when
	// configured) retains the full set.
	if len(r.announcements) > maxAnnouncements {
		r.announcements = append([]Announcement(nil), r.announcements[len(r.announcements)-maxAnnouncements:]...)
	}
	r.mu.Unlock()
	if r.store != nil {
		_ = r.store.AppendAnnouncement(storage.Announcement{
			ID: a.ID, ChannelID: r.ID, AuthorUID: a.AuthorUID,
			AuthorName: a.AuthorName, Text: a.Text, Time: a.Time,
		})
	}
	r.broadcast(Event{Type: EventAnnouncement, Announcement: &a})
	return a
}

// --- Membership & moderation ---

// Join adds uid as a member. When the channel requires approval, the request is
// instead queued and pending is returned true. Banned users cannot join, and a
// channel that disallows joining rejects new members.
func (r *Room) Join(uid, name string) (pending bool, err error) {
	r.mu.Lock()
	if r.dissolved {
		r.mu.Unlock()
		return false, ErrChannelNotFound
	}
	if r.banned[uid] {
		r.mu.Unlock()
		return false, errors.New("you are banned from this channel")
	}
	r.rememberName(uid, name)
	if r.isMember(uid) {
		rec := r.record()
		r.mu.Unlock()
		r.persist(rec)
		return false, nil
	}
	if !r.settings.AllowJoin {
		r.mu.Unlock()
		return false, errors.New("this channel is not accepting new members")
	}
	if r.settings.RequireApproval {
		r.pending[uid] = name
		rec := r.record()
		r.mu.Unlock()
		r.persist(rec)
		return true, nil
	}
	r.members[uid] = true
	rec := r.record()
	r.mu.Unlock()
	r.persist(rec)
	return false, nil
}

// Leave removes uid's membership and admin role. Owners cannot leave; they must
// dissolve the channel instead.
func (r *Room) Leave(uid string) {
	r.mu.Lock()
	if r.isOwner(uid) {
		r.mu.Unlock()
		return
	}
	delete(r.members, uid)
	delete(r.admins, uid)
	delete(r.pending, uid)
	rec := r.record()
	r.mu.Unlock()
	r.persist(rec)
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
			name, ok := r.pending[targetUID]
			if !ok {
				return errors.New("no such pending request")
			}
			r.rememberName(targetUID, name)
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
	var rec storage.Channel
	if changed {
		rec = r.record()
	}
	r.mu.Unlock()
	if err == nil && changed {
		r.persist(rec)
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

// Member is a single entry in the moderation roster: a person known to the
// channel together with their current standing.
type Member struct {
	UID    string `json:"uid"`
	Name   string `json:"name"`
	Owner  bool   `json:"owner"`
	Admin  bool   `json:"admin"`
	Member bool   `json:"member"`
	Muted  bool   `json:"muted"`
	Banned bool   `json:"banned"`
}

// Members returns the moderation roster: everyone the channel knows about
// (owner, members, admins, and anyone muted or banned), each with their current
// standing. This is the authoritative list admins moderate against, independent
// of who happens to have spoken recently.
func (r *Room) Members() []Member {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seen := make(map[string]bool)
	add := func(uid string) {
		if uid != "" {
			seen[uid] = true
		}
	}
	add(r.ownerUID)
	for uid := range r.members {
		add(uid)
	}
	for uid := range r.admins {
		add(uid)
	}
	for uid := range r.muted {
		add(uid)
	}
	for uid := range r.banned {
		add(uid)
	}

	out := make([]Member, 0, len(seen))
	for uid := range seen {
		name := r.names[uid]
		if name == "" {
			name = "user"
		}
		out = append(out, Member{
			UID:    uid,
			Name:   name,
			Owner:  r.isOwner(uid),
			Admin:  r.isAdmin(uid),
			Member: r.isMember(uid),
			Muted:  r.muted[uid],
			Banned: r.banned[uid],
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Owner != out[j].Owner {
			return out[i].Owner
		}
		if out[i].Admin != out[j].Admin {
			return out[i].Admin
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
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
	rec := r.record()
	r.mu.Unlock()
	r.persist(rec)
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
	limits    Limits
	store     storage.Store
}

// NewHub returns an empty Hub with no durable backing. maxPerUID is the number
// of channels a single owner may have active at once (<= 0 means unlimited).
func NewHub(maxPerUID int) *Hub {
	return NewHubWithStore(maxPerUID, storage.NewMemory(), DefaultLimits)
}

// NewHubWithStore returns a Hub backed by the given Store and bounded by the
// given Limits, restoring any channels the store has persisted. A memory Store
// yields an empty, process-local hub.
func NewHubWithStore(maxPerUID int, store storage.Store, limits Limits) *Hub {
	if store == nil {
		store = storage.NewMemory()
	}
	h := &Hub{
		byKey:     make(map[string]*Room),
		byID:      make(map[string]*Room),
		usedIDs:   make(map[string]bool),
		ownerNum:  make(map[string]int),
		maxPerUID: maxPerUID,
		limits:    limits.withDefaults(),
		store:     store,
	}
	h.restore()
	return h
}

// restore rebuilds channels (and their history) from the durable store.
func (h *Hub) restore() {
	channels, err := h.store.LoadChannels()
	if err != nil || len(channels) == 0 {
		return
	}
	for _, c := range channels {
		r := newRoom(c.Key, c.ID)
		r.applyLimits(h.limits)
		r.store = h.store
		r.ownerUID = c.OwnerUID
		r.settings = Settings{
			Name:            c.Name,
			Description:     c.Description,
			Visibility:      Visibility(c.Visibility),
			ListPublic:      c.ListPublic,
			AllowJoin:       c.AllowJoin,
			RequireApproval: c.RequireApproval,
			AllowSpeak:      c.AllowSpeak,
		}
		if r.settings.Name == "" {
			r.settings.Name = c.Key
		}
		setBools(r.admins, c.Admins)
		setBools(r.members, c.Members)
		setBools(r.banned, c.Banned)
		setBools(r.muted, c.Muted)
		for uid, name := range c.Names {
			r.names[uid] = name
		}
		for uid, name := range c.Pending {
			r.pending[uid] = name
		}
		// Restore message and announcement history (file blobs load lazily).
		// Only the most recent MaxMessagesPerChannel are kept resident so a
		// large on-disk history cannot blow the in-memory budget on restart.
		if msgs, err := h.store.LoadMessages(c.ID); err == nil {
			if r.maxMessages > 0 && len(msgs) > r.maxMessages {
				msgs = msgs[len(msgs)-r.maxMessages:]
			}
			for _, m := range msgs {
				r.messages = append(r.messages, Message{
					ID: m.ID, Kind: MessageKind(m.Kind), Sender: m.Sender, SenderUID: m.SenderUID,
					Text: m.Text, Mentions: m.Mentions, Preview: m.Preview,
					FileID: m.FileID, FileName: m.FileName, FileSize: m.FileSize, FileType: m.FileType,
					Time: m.Time,
				})
			}
		}
		if anns, err := h.store.LoadAnnouncements(c.ID); err == nil {
			if len(anns) > maxAnnouncements {
				anns = anns[len(anns)-maxAnnouncements:]
			}
			for _, a := range anns {
				r.announcements = append(r.announcements, Announcement{
					ID: a.ID, AuthorUID: a.AuthorUID, AuthorName: a.AuthorName, Text: a.Text, Time: a.Time,
				})
			}
		}
		h.byKey[c.Key] = r
		h.byID[c.ID] = r
		h.usedIDs[c.ID] = true
		if c.OwnerUID != "" {
			h.ownerNum[c.OwnerUID]++
		}
	}
}

func setBools(m map[string]bool, keys []string) {
	for _, k := range keys {
		if k != "" {
			m[k] = true
		}
	}
}

// Room returns the channel for key, creating an ownerless, open, public channel
// on demand. This preserves NekoDrop's quick, account-free ad-hoc rooms.
func (h *Hub) Room(key string) *Room {
	h.mu.Lock()
	if r, ok := h.byKey[key]; ok {
		h.mu.Unlock()
		return r
	}
	r := h.createLocked(key)
	rec := r.record()
	h.mu.Unlock()
	r.persist(rec)
	return r
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
	rec := r.record()
	r.persist(rec)
	return r, nil
}

func (h *Hub) createLocked(key string) *Room {
	h.reclaimLocked()
	id := h.newChannelID()
	r := newRoom(key, id)
	r.applyLimits(h.limits)
	r.store = h.store
	h.byKey[key] = r
	h.byID[id] = r
	return r
}

// reclaimLocked bounds the number of live channels. When the hub is at its cap,
// it evicts the least-recently-active ownerless channel that currently has no
// live connections. Owned channels and channels with active subscribers are
// never evicted, so reclamation only sweeps up abandoned ad-hoc rooms — the
// channels an attacker can spawn for free by hitting unique keys. The caller
// holds h.mu.
func (h *Hub) reclaimLocked() {
	if h.limits.MaxChannels <= 0 || len(h.byKey) < h.limits.MaxChannels {
		return
	}
	var victim *Room
	var victimKey string
	var victimSeen time.Time
	for k, r := range h.byKey {
		r.mu.RLock()
		evictable := r.ownerUID == "" && len(r.subscribers) == 0
		la := r.lastActive
		r.mu.RUnlock()
		if !evictable {
			continue
		}
		if victim == nil || la.Before(victimSeen) {
			victim, victimKey, victimSeen = r, k, la
		}
	}
	if victim == nil {
		return // nothing safely evictable; tolerate a soft over-cap
	}
	delete(h.byKey, victimKey)
	delete(h.byID, victim.ID)
	// A reclaimed ad-hoc room was never a deliberately "retired" channel, so its
	// ID may be reissued; releasing it keeps usedIDs from growing without bound.
	delete(h.usedIDs, victim.ID)
	victim.mu.Lock()
	victim.dissolved = true
	victim.mu.Unlock()
	if h.store != nil {
		_ = h.store.DeleteChannel(victim.ID)
	}
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
	store := h.store
	h.mu.Unlock()
	if store != nil {
		_ = store.DeleteChannel(r.ID)
	}

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

// OwnedBy returns the channels owned by ownerUID (including private and
// unlisted ones), most-online first. This powers each visitor's "your
// channels" view on the landing page.
func (h *Hub) OwnedBy(ownerUID string) []Info {
	if ownerUID == "" {
		return nil
	}
	h.mu.Lock()
	rooms := make([]*Room, 0, len(h.byKey))
	for _, r := range h.byKey {
		rooms = append(rooms, r)
	}
	h.mu.Unlock()

	out := make([]Info, 0)
	for _, r := range rooms {
		info := r.Info()
		if info.OwnerUID == ownerUID && !info.Dissolved {
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
