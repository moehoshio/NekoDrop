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
	"sort"
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

// DefaultMaxUsers bounds how many identities are retained in memory. Because a
// fresh identity is minted for every cookieless request, an unbounded registry
// is a denial-of-service vector; the oldest unnamed identities are evicted once
// the cap is reached.
const DefaultMaxUsers = 100_000

// User is a single known visitor. Token is the secret stored in the client's
// cookie; UID is the short, public, stable identifier shown in the UI. Named
// reports whether the visitor has deliberately chosen their current name (as
// opposed to keeping the auto-assigned default).
//
// MigrateCode, when non-empty, is the user's account-migration code: a second
// bearer secret, opt-in and off by default, that another browser can present
// to adopt this identity (see Registry.ByMigrationCode). Like Token it is
// never serialized to clients except through the dedicated migration API.
type User struct {
	Token       string `json:"-"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	Named       bool   `json:"named"`
	MigrateCode string `json:"-"`
}

// Label returns the canonical "name#uid" rendering used throughout the UI.
func (u *User) Label() string { return u.Name + "#" + u.UID }

// Registry is a concurrency-safe in-memory store of users keyed by token,
// optionally backed by a durable Store. The number of resident identities is
// bounded by maxUsers to cap memory under cookieless traffic.
type Registry struct {
	mu       sync.RWMutex
	byToken  map[string]*User
	byCode   map[string]*User // migration code -> user, for account migration
	order    []string         // tokens in insertion order, for eviction
	nextUID  int
	maxUsers int
	store    storage.Store
}

// NewRegistry returns an empty Registry with no durable backing.
func NewRegistry() *Registry { return NewRegistryWithStore(storage.NewMemory(), DefaultMaxUsers) }

// NewRegistryWithStore returns a Registry whose users are loaded from and
// persisted to the given Store, retaining at most maxUsers identities in
// memory. A memory Store yields the original process-local behaviour; a
// non-positive maxUsers falls back to DefaultMaxUsers.
func NewRegistryWithStore(store storage.Store, maxUsers int) *Registry {
	if store == nil {
		store = storage.NewMemory()
	}
	if maxUsers <= 0 {
		maxUsers = DefaultMaxUsers
	}
	r := &Registry{
		byToken:  make(map[string]*User),
		byCode:   make(map[string]*User),
		nextUID:  firstUID,
		maxUsers: maxUsers,
		store:    store,
	}
	if users, err := store.LoadUsers(); err == nil {
		for _, su := range users {
			u := &User{Token: su.Token, UID: su.UID, Name: su.Name, Named: su.Named, MigrateCode: su.MigrateCode}
			r.byToken[u.Token] = u
			if u.MigrateCode != "" {
				r.byCode[u.MigrateCode] = u
			}
			r.order = append(r.order, u.Token)
			if n, err := strconv.Atoi(u.UID); err == nil && n >= r.nextUID {
				r.nextUID = n + 1
			}
		}
	}
	return r
}

// evictLocked drops the oldest identity to stay within maxUsers, preferring
// unnamed (anonymous) identities that never opted into account migration, so
// that users who deliberately named themselves or set up a migration code
// survive longest. It reports whether an identity was removed. The caller
// holds the write lock.
func (r *Registry) evictLocked() bool {
	if r.maxUsers <= 0 || len(r.byToken) < r.maxUsers {
		return false
	}
	// Prefer the oldest anonymous, non-migratable user; fall back to the oldest
	// user overall.
	victim := -1
	for i, tok := range r.order {
		u := r.byToken[tok]
		if u == nil {
			continue
		}
		if !u.Named && u.MigrateCode == "" {
			victim = i
			break
		}
		if victim == -1 {
			victim = i
		}
	}
	if victim == -1 {
		return false
	}
	tok := r.order[victim]
	r.order = append(r.order[:victim], r.order[victim+1:]...)
	if u := r.byToken[tok]; u != nil && u.MigrateCode != "" {
		delete(r.byCode, u.MigrateCode)
	}
	delete(r.byToken, tok)
	return true
}

// SetMaxUsers changes the in-memory identity cap at runtime, evicting down to
// the new bound immediately. A non-positive value restores the default.
func (r *Registry) SetMaxUsers(n int) {
	if n <= 0 {
		n = DefaultMaxUsers
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxUsers = n
	for len(r.byToken) > r.maxUsers {
		if !r.evictLocked() {
			break
		}
	}
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
	r.evictLocked()
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
	r.order = append(r.order, u.Token)
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

// --- Account migration ---
//
// Migration is opt-in and off by default for every user. Enabling it mints a
// persistent migration code — a bearer secret separate from the cookie token —
// that the user can enter in another browser to adopt (inherit) this identity
// there. The code stays valid until the user disables migration or generates a
// new one.

// MigrationCode returns the migration code of the user bound to token, or ""
// when migration is disabled or the token is unknown.
func (r *Registry) MigrationCode(token string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if u := r.byToken[token]; u != nil {
		return u.MigrateCode
	}
	return ""
}

// EnableMigration turns on account migration for the user bound to token,
// minting a fresh migration code (invalidating any previous one). It returns
// the new code, or ok=false when the token is unknown.
func (r *Registry) EnableMigration(token string) (code string, ok bool) {
	r.mu.Lock()
	u := r.byToken[token]
	if u == nil {
		r.mu.Unlock()
		return "", false
	}
	if u.MigrateCode != "" {
		delete(r.byCode, u.MigrateCode)
	}
	code = newToken()
	u.MigrateCode = code
	r.byCode[code] = u
	snapshot := *u
	r.mu.Unlock()

	r.persist(&snapshot)
	return code, true
}

// DisableMigration turns off account migration for the user bound to token,
// invalidating their migration code. It reports whether the token was known.
func (r *Registry) DisableMigration(token string) bool {
	r.mu.Lock()
	u := r.byToken[token]
	if u == nil {
		r.mu.Unlock()
		return false
	}
	if u.MigrateCode != "" {
		delete(r.byCode, u.MigrateCode)
		u.MigrateCode = ""
	}
	snapshot := *u
	r.mu.Unlock()

	r.persist(&snapshot)
	return true
}

// ByMigrationCode returns the user whose migration code matches, or nil. The
// caller typically re-binds its identity cookie to the returned user's token,
// completing the migration.
func (r *Registry) ByMigrationCode(code string) *User {
	if code == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byCode[code]
}

// --- Administration ---

// Len returns the number of identities currently held in memory.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byToken)
}

// ByUID returns the resident user with the given public UID, or nil. Intended
// for occasional administrative lookups; it scans the registry.
func (r *Registry) ByUID(uid string) *User {
	if uid == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, u := range r.byToken {
		if u.UID == uid {
			return u
		}
	}
	return nil
}

// List returns snapshots of up to limit users whose name or UID contains query
// (case-insensitive; an empty query matches everyone), ordered by UID. It backs
// the admin panel's user roster; the snapshots carry no secrets to serialize
// (Token and MigrateCode are excluded from JSON), but callers should still
// treat them as admin-only data.
func (r *Registry) List(query string, limit int) []User {
	if limit <= 0 {
		limit = 100
	}
	query = strings.ToLower(strings.TrimSpace(query))

	r.mu.RLock()
	out := make([]User, 0, min(limit, len(r.byToken)))
	uids := make([]int, 0, len(r.byToken))
	byUID := make(map[int]*User, len(r.byToken))
	for _, u := range r.byToken {
		if query != "" && !strings.Contains(strings.ToLower(u.Name), query) &&
			!strings.Contains(u.UID, query) {
			continue
		}
		if n, err := strconv.Atoi(u.UID); err == nil {
			uids = append(uids, n)
			byUID[n] = u
		}
	}
	sort.Ints(uids)
	for _, n := range uids {
		if len(out) >= limit {
			break
		}
		out = append(out, *byUID[n])
	}
	r.mu.RUnlock()
	return out
}

func (r *Registry) persist(u *User) {
	if r.store == nil {
		return
	}
	_ = r.store.SaveUser(storage.User{Token: u.Token, UID: u.UID, Name: u.Name, Named: u.Named, MigrateCode: u.MigrateCode})
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
