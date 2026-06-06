// Package user implements NekoDrop's lightweight identity model. NekoDrop has
// no passwords or sign-up: a visitor is issued an opaque token (stored in a
// cookie) the first time they are seen, and that token is bound to a stable
// public UID. The UID is rendered after the display name (e.g. "alice#123") so
// that two people sharing a nickname remain distinguishable, and so mentions
// can target a specific person.
package user

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

// firstUID is the first public UID handed out. Starting at a three-digit value
// keeps the "alice#123" rendering visually tidy from the very first user.
const firstUID = 100

// User is a single known visitor. Token is the secret stored in the client's
// cookie; UID is the short, public, stable identifier shown in the UI.
type User struct {
	Token string `json:"-"`
	UID   string `json:"uid"`
	Name  string `json:"name"`
}

// Label returns the canonical "name#uid" rendering used throughout the UI.
func (u *User) Label() string { return u.Name + "#" + u.UID }

// Registry is a concurrency-safe in-memory store of users keyed by token.
type Registry struct {
	mu      sync.RWMutex
	byToken map[string]*User
	nextUID int
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byToken: make(map[string]*User),
		nextUID: firstUID,
	}
}

// Get returns the user bound to token, or nil if the token is unknown.
func (r *Registry) Get(token string) *User {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byToken[token]
}

// Create mints a brand new user with a fresh token and the next sequential
// UID. The optional name is sanitized; an empty name falls back to "anonymous".
func (r *Registry) Create(name string) *User {
	r.mu.Lock()
	defer r.mu.Unlock()

	uid := strconv.Itoa(r.nextUID)
	r.nextUID++

	u := &User{
		Token: newToken(),
		UID:   uid,
		Name:  SanitizeName(name),
	}
	r.byToken[u.Token] = u
	return u
}

// Rename updates the display name of the user bound to token. The UID is never
// changed, so existing mentions and history remain valid.
func (r *Registry) Rename(token, name string) (*User, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.byToken[token]
	if !ok {
		return nil, false
	}
	u.Name = SanitizeName(name)
	return u, true
}

// SanitizeName trims a display name and applies a default and length limit.
// The result is only ever rendered as text on the client, never as HTML.
func SanitizeName(name string) string {
	name = strings.TrimSpace(name)
	// Collapse any internal whitespace so a name cannot smuggle newlines.
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "anonymous"
	}
	if len([]rune(name)) > 32 {
		name = string([]rune(name)[:32])
	}
	return name
}

// newToken returns a 256-bit random hex token suitable for a cookie value.
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail; fall back to a time-based value.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b)
}
