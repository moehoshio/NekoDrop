// Package user implements NekoDrop's lightweight identity model. NekoDrop has
// no passwords or sign-up: a visitor is issued an opaque token (stored in a
// cookie) the first time they are seen, and that token is bound to a stable
// public UID. The UID is rendered after the display name (e.g. "alice#123") so
// that two people sharing a nickname remain distinguishable, and so mentions
// can target a specific person.
//
// A visitor who has never chosen a name keeps a default, auto-assigned name
// (DefaultName). Because the default is a distinct word, a never-named guest
// ("Guest#123") is always visually distinguishable from someone who has
// deliberately typed a name — even a name like "anonymous".
package user

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moehoshio/NekoDrop/internal/storage"
)

// firstUID is the first public UID handed out. Starting at a three-digit value
// keeps the "alice#123" rendering visually tidy from the very first user.
const firstUID = 100

// DefaultName is the display name assigned to a visitor who has not chosen one.
// It is intentionally distinct from any common deliberate nickname so the
// unnamed state is always obvious in the UI.
const DefaultName = "Guest"

// User is a single known visitor. Token is the secret stored in the client's
// cookie; UID is the short, public, stable identifier shown in the UI. Named
// reports whether the visitor has deliberately chosen their current name (as
// opposed to keeping the auto-assigned default).
type User struct {
	Token string `json:"-"`
	UID   string `json:"uid"`
	Name  string `json:"name"`
	Named bool   `json:"named"`
}

// Label returns the canonical "name#uid" rendering used throughout the UI.
func (u *User) Label() string { return u.Name + "#" + u.UID }

// Registry is a concurrency-safe in-memory store of users keyed by token,
// optionally backed by a durable Store.
type Registry struct {
	mu      sync.RWMutex
	byToken map[string]*User
	nextUID int
	store   storage.Store
}

// NewRegistry returns an empty Registry with no durable backing.
func NewRegistry() *Registry { return NewRegistryWithStore(storage.NewMemory()) }

// NewRegistryWithStore returns a Registry whose users are loaded from and
// persisted to the given Store. A memory Store yields the original
// process-local behaviour.
func NewRegistryWithStore(store storage.Store) *Registry {
	if store == nil {
		store = storage.NewMemory()
	}
	r := &Registry{
		byToken: make(map[string]*User),
		nextUID: firstUID,
		store:   store,
	}
	if users, err := store.LoadUsers(); err == nil {
		for _, su := range users {
			u := &User{Token: su.Token, UID: su.UID, Name: su.Name, Named: su.Named}
			r.byToken[u.Token] = u
			if n, err := strconv.Atoi(u.UID); err == nil && n >= r.nextUID {
				r.nextUID = n + 1
			}
		}
	}
	return r
}

// Get returns the user bound to token, or nil if the token is unknown.
func (r *Registry) Get(token string) *User {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byToken[token]
}

// Create mints a brand new user with a fresh token and the next sequential
// UID. A non-empty name is sanitized and the user is marked as named; an empty
// name leaves the user on the default name until they choose one.
func (r *Registry) Create(name string) *User {
	r.mu.Lock()
	uid := strconv.Itoa(r.nextUID)
	r.nextUID++

	named := strings.TrimSpace(name) != ""
	u := &User{
		Token: newToken(),
		UID:   uid,
		Name:  resolveName(name),
		Named: named,
	}
	r.byToken[u.Token] = u
	r.mu.Unlock()

	r.persist(u)
	return u
}

// Rename updates the display name of the user bound to token. The UID is never
// changed, so existing mentions and history remain valid. A non-empty name
// marks the user as named; clearing the name reverts to the default and the
// unnamed state.
func (r *Registry) Rename(token, name string) (*User, bool) {
	r.mu.Lock()
	u, ok := r.byToken[token]
	if !ok {
		r.mu.Unlock()
		return nil, false
	}
	u.Name = resolveName(name)
	u.Named = strings.TrimSpace(name) != ""
	snapshot := *u
	r.mu.Unlock()

	r.persist(&snapshot)
	return u, true
}

func (r *Registry) persist(u *User) {
	if r.store == nil {
		return
	}
	_ = r.store.SaveUser(storage.User{Token: u.Token, UID: u.UID, Name: u.Name, Named: u.Named})
}

// resolveName sanitizes a chosen name, falling back to the default when empty.
func resolveName(name string) string {
	if s := SanitizeName(name); s != "" {
		return s
	}
	return DefaultName
}

// SanitizeName trims a display name and applies a length limit. An empty result
// signals that no name was provided; callers decide how to render that. The
// result is only ever rendered as text on the client, never as HTML.
func SanitizeName(name string) string {
	name = strings.TrimSpace(name)
	// Collapse any internal whitespace so a name cannot smuggle newlines.
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return ""
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
