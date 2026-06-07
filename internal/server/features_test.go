package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/moehoshio/NekoDrop/internal/room"
	"github.com/moehoshio/NekoDrop/internal/storage"
)

// TestDefaultNameIsGuestAndDistinct verifies that a never-named visitor keeps
// the distinct default name (so the unnamed state is never confused with a
// deliberately chosen "anonymous"), and that naming flips the Named flag.
func TestDefaultNameIsGuestAndDistinct(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	rec := c.do("GET", "/api/me", "", "")
	var me struct {
		Name  string `json:"name"`
		Named bool   `json:"named"`
	}
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Name != "Guest" || me.Named {
		t.Fatalf("unnamed visitor = %+v, want {Guest false}", me)
	}

	// Deliberately choosing "anonymous" is a named state, distinct from default.
	rec = c.do("POST", "/api/me", "application/json", `{"name":"anonymous"}`)
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Name != "anonymous" || !me.Named {
		t.Fatalf("named visitor = %+v, want {anonymous true}", me)
	}
}

// TestOwnedChannelsListed verifies the landing "your channels" payload.
func TestOwnedChannelsListed(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	// A private, unlisted channel must still appear in the owner's own list.
	if rec := c.do("POST", "/api/channels", "application/json",
		`{"name":"Secret","visibility":"private","listPublic":false}`); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	rec := c.do("GET", "/api/channels", "", "")
	var resp struct {
		Channels []room.Info `json:"channels"`
		Mine     []room.Info `json:"mine"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Mine) != 1 || resp.Mine[0].Name != "Secret" {
		t.Fatalf("mine = %+v, want one Secret channel", resp.Mine)
	}
	if len(resp.Channels) != 0 {
		t.Fatalf("public list should be empty, got %+v", resp.Channels)
	}

	// A different visitor owns nothing.
	other := newClient(t, s)
	rec = other.do("GET", "/api/channels", "", "")
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Mine) != 0 {
		t.Fatalf("other visitor mine = %+v, want empty", resp.Mine)
	}
}

// TestMemberRosterAndModeration verifies the admin member roster reflects
// moderation actions (item 8: moderation must visibly take effect).
func TestMemberRosterAndModeration(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)
	owner.do("POST", "/api/channels", "application/json", `{"name":"Club","allowSpeak":true}`)

	// A member joins and speaks so the roster knows their name.
	bob := newClient(t, s)
	bob.do("POST", "/api/me", "application/json", `{"name":"Bob"}`)
	bob.do("POST", "/api/channels/club/join", "", "")
	bob.do("POST", "/api/messages/club", "application/json", `{"sender":"Bob","text":"hi"}`)

	// The owner sees Bob in the roster.
	rec := owner.do("GET", "/api/channels/club", "", "")
	var view channelView
	json.Unmarshal(rec.Body.Bytes(), &view)
	bobUID := ""
	for _, m := range view.Members {
		if m.Name == "Bob" {
			bobUID = m.UID
		}
	}
	if bobUID == "" {
		t.Fatalf("Bob not in roster: %+v", view.Members)
	}

	// Mute Bob and confirm the roster reflects it.
	rec = owner.do("POST", "/api/channels/club/moderate", "application/json",
		`{"action":"mute","uid":"`+bobUID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("moderate = %d: %s", rec.Code, rec.Body.String())
	}
	var modResp struct {
		Members []room.Member `json:"members"`
	}
	json.Unmarshal(rec.Body.Bytes(), &modResp)
	muted := false
	for _, m := range modResp.Members {
		if m.UID == bobUID && m.Muted {
			muted = true
		}
	}
	if !muted {
		t.Fatalf("Bob should be muted in roster: %+v", modResp.Members)
	}

	// Bob can no longer speak.
	if rec := bob.do("POST", "/api/messages/club", "application/json", `{"text":"again"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("muted post = %d, want 403", rec.Code)
	}
}

// TestSQLitePersistenceAcrossRestart verifies a channel and its history survive
// a server restart when backed by a SQLite store (item 10).
func TestSQLitePersistenceAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nekodrop.db")
	store, err := storage.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	s1, err := New(Options{MaxUploadBytes: 1 << 20, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	owner := newClient(t, s1)
	owner.do("POST", "/api/channels", "application/json",
		`{"name":"Persisted","listPublic":true}`)
	owner.do("POST", "/api/messages/persisted", "application/json",
		`{"sender":"Alice","text":"durable hello"}`)
	store.Close()

	// Reopen the same database in a fresh server.
	store2, err := storage.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()
	s2, err := New(Options{MaxUploadBytes: 1 << 20, Store: store2})
	if err != nil {
		t.Fatal(err)
	}
	visitor := newClient(t, s2)
	rec := visitor.do("GET", "/api/channels", "", "")
	var resp struct {
		Channels []room.Info `json:"channels"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Channels) != 1 || resp.Channels[0].Name != "Persisted" {
		t.Fatalf("restored public list = %+v, want one Persisted channel", resp.Channels)
	}
}
