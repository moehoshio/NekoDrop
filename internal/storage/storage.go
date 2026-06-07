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

// User is the persisted form of a visitor identity.
type User struct {
	Token string
	UID   string
	Name  string
	Named bool
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
	Preview   bool
	FileID    string
	FileName  string
	FileSize  int64
	FileType  string
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

	// Files.
	LoadFile(id string) (File, bool, error)
	SaveFile(File) error

	// Announcements.
	LoadAnnouncements(channelID string) ([]Announcement, error)
	AppendAnnouncement(Announcement) error

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
func (*Memory) LoadFile(string) (File, bool, error)              { return File{}, false, nil }
func (*Memory) SaveFile(File) error                              { return nil }
func (*Memory) LoadAnnouncements(string) ([]Announcement, error) { return nil, nil }
func (*Memory) AppendAnnouncement(Announcement) error            { return nil }
func (*Memory) Close() error                                     { return nil }
