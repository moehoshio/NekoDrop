// Package room implements the in-memory chat-room (session) model used by
// NekoDrop. Each room is identified by a key derived from a URL path or a
// join code. Rooms hold a history of messages (text and file metadata),
// the uploaded file payloads, and the set of subscribers that receive
// real-time updates.
package room

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// MessageKind enumerates the kinds of messages a room can hold.
type MessageKind string

const (
	// KindText is a plain text chat message.
	KindText MessageKind = "text"
	// KindFile is a shared file made available for download.
	KindFile MessageKind = "file"
)

// Message is a single entry in a room's history. It is safe to serialize to
// JSON and send to clients; file payloads are stored separately and referenced
// through FileID.
type Message struct {
	ID       string      `json:"id"`
	Kind     MessageKind `json:"kind"`
	Sender   string      `json:"sender"`
	Text     string      `json:"text,omitempty"`
	FileID   string      `json:"fileId,omitempty"`
	FileName string      `json:"fileName,omitempty"`
	FileSize int64       `json:"fileSize,omitempty"`
	Time     time.Time   `json:"time"`
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

// ErrFileNotFound is returned when a requested file does not exist in a room.
var ErrFileNotFound = errors.New("file not found")

// Room is a single chat session. All exported methods are safe for concurrent
// use by multiple goroutines.
type Room struct {
	Key string

	mu          sync.RWMutex
	messages    []Message
	files       map[string]*File
	subscribers map[chan Message]struct{}
}

func newRoom(key string) *Room {
	return &Room{
		Key:         key,
		files:       make(map[string]*File),
		subscribers: make(map[chan Message]struct{}),
	}
}

// Subscribe registers a new subscriber and returns a snapshot of the existing
// history, a channel that receives messages published after the call, and an
// unsubscribe function. The caller must invoke the returned function when
// finished to release resources.
func (r *Room) Subscribe() (history []Message, ch <-chan Message, cancel func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	history = make([]Message, len(r.messages))
	copy(history, r.messages)

	c := make(chan Message, 32)
	r.subscribers[c] = struct{}{}

	cancel = func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if _, ok := r.subscribers[c]; ok {
			delete(r.subscribers, c)
			close(c)
		}
	}
	return history, c, cancel
}

// publish appends a message to the history and fans it out to subscribers.
// Slow subscribers that cannot keep up are skipped for that message rather
// than blocking the publisher.
func (r *Room) publish(m Message) {
	r.mu.Lock()
	r.messages = append(r.messages, m)
	subs := make([]chan Message, 0, len(r.subscribers))
	for c := range r.subscribers {
		subs = append(subs, c)
	}
	r.mu.Unlock()

	for _, c := range subs {
		select {
		case c <- m:
		default:
		}
	}
}

// AddText records a text message from sender and broadcasts it.
func (r *Room) AddText(sender, text string) Message {
	m := Message{
		ID:     newID(),
		Kind:   KindText,
		Sender: sender,
		Text:   text,
		Time:   time.Now().UTC(),
	}
	r.publish(m)
	return m
}

// AddFile stores a file payload, records a file message referencing it, and
// broadcasts the message.
func (r *Room) AddFile(sender, name, contentType string, data []byte) Message {
	f := &File{
		ID:          newID(),
		Name:        name,
		ContentType: contentType,
		Data:        data,
	}

	r.mu.Lock()
	r.files[f.ID] = f
	r.mu.Unlock()

	m := Message{
		ID:       newID(),
		Kind:     KindFile,
		Sender:   sender,
		FileID:   f.ID,
		FileName: name,
		FileSize: f.Size(),
		Time:     time.Now().UTC(),
	}
	r.publish(m)
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

// Subscribers returns the current number of active subscribers.
func (r *Room) Subscribers() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.subscribers)
}

// Hub manages the set of live rooms, creating them on demand.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{rooms: make(map[string]*Room)}
}

// Room returns the room for key, creating it if it does not yet exist.
func (h *Hub) Room(key string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[key]
	if !ok {
		r = newRoom(key)
		h.rooms[key] = r
	}
	return r
}

// Len returns the number of live rooms.
func (h *Hub) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.rooms)
}

// newID returns a short random hex identifier.
func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a time-based value.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
