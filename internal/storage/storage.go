// Package storage defines NekoDrop's durable persistence layer and the
// backends that implement it. NekoDrop keeps all live state (subscribers,
// presence, event fan-out) in memory; the Store is responsible only for the
// data worth surviving a restart: users, channel definitions and membership,
// the message history, uploaded files and announcements.
//
// Three backends are available, selected by configuration:
//
//   - "memory" (the default) is a no-op store. Nothing is persisted and the
//     server behaves exactly as it did before a Store existed.
//   - "sqlite" persists everything to a single, embedded, pure-Go SQLite
//     database file (no cgo, so release cross-compilation keeps working).
//   - "mysql" persists everything to a MySQL/MariaDB server.
//
// The storage DTOs are deliberately independent of the room and user packages
// so that those packages can depend on storage without creating an import
// cycle.
package storage

import (
	"fmt"
	"time"
)

// User is the persisted form of a visitor identity. MigrateCode, when
// non-empty, is the user's account-migration code: a second, user-managed
// bearer secret that lets another browser adopt this identity.
type User struct {
	Token       string
	UID         string
	Name        string
	Named       bool
	MigrateCode string
}

// Channel is the persisted form of a channel's definition and membership.
// Live state (online presence, subscribers) is never persisted.
type Channel struct {
	ID              string
	Key             string
	OwnerUID        string
	Name            string
	Description     string
	Visibility      string
	ListPublic      bool
	AllowJoin       bool
	RequireApproval bool
	AllowSpeak      bool
	Admins          []string
	Members         []string
	Banned          []string
	Muted           []string
	Names           map[string]string // uid -> last seen display name
	Nicks           map[string]string // uid -> per-channel display nickname
	Pending         map[string]string // uid -> requested display name
}

// Message is the persisted form of a single chat entry.
type Message struct {
	ID        string
	ChannelID string
	Kind      string
	Sender    string
	SenderUID string
	Text      string
	Mentions  []string
	ReplyTo   string
	Preview   bool
	FileID    string
	FileName  string
	FileSize  int64
	FileType  string
	Edited    bool
	Time      time.Time
}

// File is the persisted form of an uploaded payload.
type File struct {
	ID          string
	ChannelID   string
	Name        string
	ContentType string
	Data        []byte
}

// Announcement is the persisted form of a pinned notice.
type Announcement struct {
	ID         string
	ChannelID  string
	AuthorUID  string
	AuthorName string
	Text       string
	Time       time.Time
}

// Stats are approximate persisted totals reported for the admin dashboard.
// The no-op memory backend reports Persistent=false and zeroes.
type Stats struct {
	// Persistent reports whether the backend actually stores anything.
	Persistent bool
	// SizeBytes is the approximate on-disk size of the database (0 = unknown).
	SizeBytes int64
	// Row counts per record type.
	Users         int
	Channels      int
	Messages      int
	Files         int
	Announcements int
	// FileBytes is the total size of persisted file payloads.
	FileBytes int64
}

// Store is the durable persistence contract. Implementations must be safe for
// concurrent use. All Load* methods return the persisted records (or empty
// slices for the no-op backend); all write methods are best-effort from the
// caller's perspective and surface errors for logging.
type Store interface {
	// Backend reports the configured backend name (memory/sqlite/mysql).
	Backend() string

	// Users.
	LoadUsers() ([]User, error)
	SaveUser(User) error

	// Channels.
	LoadChannels() ([]Channel, error)
	SaveChannel(Channel) error
	DeleteChannel(id string) error

	// Messages.
	LoadMessages(channelID string) ([]Message, error)
	AppendMessage(Message) error
	DeleteMessage(id string) error
	// DeleteMessagesBySender removes every message a sender posted in a channel,
	// along with the file payloads those messages owned.
	DeleteMessagesBySender(channelID, senderUID string) error

	// Files.
	LoadFile(id string) (File, bool, error)
	SaveFile(File) error
	DeleteFile(id string) error

	// Announcements.
	LoadAnnouncements(channelID string) ([]Announcement, error)
	AppendAnnouncement(Announcement) error

	// Key-value settings. LoadKV reports whether the key exists; SaveKV
	// overwrites any previous value. Used for small server-side state such as
	// the admin panel's bans and runtime configuration overrides.
	LoadKV(key string) (value string, ok bool, err error)
	SaveKV(key, value string) error

	// Stats reports approximate persisted totals for the admin dashboard.
	Stats() (Stats, error)

	// Close releases any underlying resources.
	Close() error
}

// Open constructs a Store for the given backend. The dsn is interpreted per
// backend: ignored for "memory", a file path for "sqlite" (e.g.
// "nekodrop.db"), and a Go MySQL DSN for "mysql"
// (e.g. "user:pass@tcp(127.0.0.1:3306)/nekodrop").
func Open(backend, dsn string) (Store, error) {
	switch backend {
	case "", "memory":
		return NewMemory(), nil
	case "sqlite":
		return openSQL("sqlite", dsn)
	case "mysql":
		return openSQL("mysql", dsn)
	default:
		return nil, fmt.Errorf("storage: unknown backend %q (want memory, sqlite or mysql)", backend)
	}
}

// Memory is the default, non-persistent Store. Every read returns nothing and
// every write is discarded, so the server keeps its original in-memory-only
// behaviour.
type Memory struct{}

// NewMemory returns a no-op in-memory Store.
func NewMemory() *Memory { return &Memory{} }

func (*Memory) Backend() string                                  { return "memory" }
func (*Memory) LoadUsers() ([]User, error)                       { return nil, nil }
func (*Memory) SaveUser(User) error                              { return nil }
func (*Memory) LoadChannels() ([]Channel, error)                 { return nil, nil }
func (*Memory) SaveChannel(Channel) error                        { return nil }
func (*Memory) DeleteChannel(string) error                       { return nil }
func (*Memory) LoadMessages(string) ([]Message, error)           { return nil, nil }
func (*Memory) AppendMessage(Message) error                      { return nil }
func (*Memory) DeleteMessage(string) error                       { return nil }
func (*Memory) DeleteMessagesBySender(string, string) error      { return nil }
func (*Memory) LoadFile(string) (File, bool, error)              { return File{}, false, nil }
func (*Memory) SaveFile(File) error                              { return nil }
func (*Memory) DeleteFile(string) error                          { return nil }
func (*Memory) LoadAnnouncements(string) ([]Announcement, error) { return nil, nil }
func (*Memory) AppendAnnouncement(Announcement) error            { return nil }
func (*Memory) LoadKV(string) (string, bool, error)              { return "", false, nil }
func (*Memory) SaveKV(string, string) error                      { return nil }
func (*Memory) Stats() (Stats, error)                            { return Stats{}, nil }
func (*Memory) Close() error                                     { return nil }
