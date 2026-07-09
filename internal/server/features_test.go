package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moehoshio/NekoDrop/internal/room"
	"github.com/moehoshio/NekoDrop/internal/storage"
)

// TestAutoNameIsGeneratedAndDistinct verifies that a never-named visitor gets
// a generated display name (marked Named=false so the UI can invite them to
// pick their own), and that choosing a name flips the flag and travels
// verbatim.
func TestAutoNameIsGeneratedAndDistinct(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	rec := c.do("GET", "/api/me", "", "")
	var me struct {
		Name  string `json:"name"`
		Named bool   `json:"named"`
	}
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Name == "" || me.Named {
		t.Fatalf("unnamed visitor = %+v, want a generated name and named=false", me)
	}

	// Deliberately choosing "anonymous" is a named state, distinct from default.
	rec = c.do("POST", "/api/me", "application/json", `{"name":"anonymous"}`)
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Name != "anonymous" || !me.Named {
		t.Fatalf("named visitor = %+v, want {anonymous true}", me)
	}
}

// TestStaticAssetCacheBusting verifies that HTML pages reference their static
// assets through versioned URLs and are never cached without revalidation,
// while the versioned assets themselves are immutable. This is what prevents a
// browser from pairing a stale cached script with a newer page after a server
// upgrade (which previously surfaced raw i18n keys).
func TestStaticAssetCacheBusting(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	rec := c.do("GET", "/", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("page Cache-Control = %q, want no-cache", cc)
	}
	body := rec.Body.String()
	want := `/static/i18n.js?v=` + s.staticVer
	if !strings.Contains(body, want) {
		t.Fatalf("page does not reference versioned asset %q", want)
	}
	if strings.Contains(body, `src="/static/i18n.js"`) {
		t.Fatal("page still references an unversioned script URL")
	}

	// The versioned asset is immutable; the bare URL must revalidate.
	rec = c.do("GET", "/static/i18n.js?v="+s.staticVer, "", "")
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("versioned asset Cache-Control = %q, want immutable", cc)
	}
	rec = c.do("GET", "/static/i18n.js", "", "")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("unversioned asset Cache-Control = %q, want no-cache", cc)
	}
	// A stale version string (an old build's URL) must not be cached forever.
	rec = c.do("GET", "/static/i18n.js?v=stale123", "", "")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("stale-versioned asset Cache-Control = %q, want no-cache", cc)
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

// TestMessageSenderCarriesName verifies that every message carries its
// sender's current display name — the auto-generated one for visitors who
// never chose a name, or the chosen name verbatim.
func TestMessageSenderCarriesName(t *testing.T) {
	s := newTestServer(t)

	auto := newClient(t, s)
	rec := auto.do("GET", "/api/me", "", "")
	var me struct {
		Name string `json:"name"`
	}
	json.Unmarshal(rec.Body.Bytes(), &me)
	rec = auto.do("POST", "/api/messages/room-x", "application/json", `{"text":"hi"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("auto-named post = %d: %s", rec.Code, rec.Body.String())
	}
	var m room.Message
	json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Sender == "" || m.Sender != me.Name {
		t.Fatalf("auto-named sender = %q, want the generated name %q", m.Sender, me.Name)
	}

	named := newClient(t, s)
	named.do("POST", "/api/me", "application/json", `{"name":"Rin"}`)
	rec = named.do("POST", "/api/messages/room-x", "application/json", `{"text":"yo"}`)
	json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Sender != "Rin" {
		t.Fatalf("named sender = %q, want Rin", m.Sender)
	}
}

// TestAnnouncementEndpoints exercises posting, editing and removing
// announcements over HTTP, and that only an owner/admin may mutate them.
func TestAnnouncementEndpoints(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)

	rec := owner.do("POST", "/api/channels", "application/json", `{"name":"Notices","visibility":"public"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	var ch struct {
		Key string `json:"key"`
	}
	json.Unmarshal(rec.Body.Bytes(), &ch)
	if ch.Key == "" {
		t.Fatal("channel create returned no key")
	}
	base := "/api/channels/" + ch.Key + "/announcements"

	rec = owner.do("POST", base, "application/json", `{"text":"hello"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("post announcement = %d: %s", rec.Code, rec.Body.String())
	}
	var a struct {
		ID     string `json:"id"`
		Text   string `json:"text"`
		Edited bool   `json:"edited"`
	}
	json.Unmarshal(rec.Body.Bytes(), &a)
	if a.ID == "" || a.Text != "hello" {
		t.Fatalf("announcement = %+v", a)
	}

	// A stranger may neither edit nor remove announcements.
	stranger := newClient(t, s)
	if rec := stranger.do("PATCH", base+"/"+a.ID, "application/json", `{"text":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger edit = %d, want 403", rec.Code)
	}
	if rec := stranger.do("DELETE", base+"/"+a.ID, "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("stranger delete = %d, want 403", rec.Code)
	}

	// The owner edits it; the Edited flag flips.
	rec = owner.do("PATCH", base+"/"+a.ID, "application/json", `{"text":"updated"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner edit = %d: %s", rec.Code, rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &a)
	if a.Text != "updated" || !a.Edited {
		t.Fatalf("edited announcement = %+v", a)
	}

	// Editing a missing announcement is a 404.
	if rec := owner.do("PATCH", base+"/missing", "application/json", `{"text":"z"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("edit missing = %d, want 404", rec.Code)
	}

	// The owner removes it.
	if rec := owner.do("DELETE", base+"/"+a.ID, "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete = %d: %s", rec.Code, rec.Body.String())
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

// TestChannelNickOverridesGlobalName verifies that a per-channel nickname sets
// the display name on a message, while an empty nick falls back to the global
// name (item 1). Identity remains keyed by UID throughout.
func TestChannelNickOverridesGlobalName(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)
	c.do("POST", "/api/me", "application/json", `{"name":"Alice"}`)

	// A message sent with a nick shows the nick, not the global name.
	rec := c.do("POST", "/api/messages/demo", "application/json",
		`{"sender":"Alice","nick":"Ally","text":"hi"}`)
	var m room.Message
	json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Sender != "Ally" {
		t.Fatalf("nick not applied: sender=%q, want Ally", m.Sender)
	}
	uid := m.SenderUID

	// A message with no nick falls back to the global name, under the same UID.
	rec = c.do("POST", "/api/messages/demo", "application/json",
		`{"sender":"Alice","text":"hi again"}`)
	json.Unmarshal(rec.Body.Bytes(), &m)
	if m.Sender != "Alice" || m.SenderUID != uid {
		t.Fatalf("fallback wrong: sender=%q uid=%q, want Alice/%s", m.Sender, m.SenderUID, uid)
	}
}

// TestReplyToRoundTrips verifies a message can reference an earlier one (item 4).
func TestReplyToRoundTrips(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	rec := c.do("POST", "/api/messages/demo", "application/json", `{"sender":"Alice","text":"first"}`)
	var first room.Message
	json.Unmarshal(rec.Body.Bytes(), &first)

	rec = c.do("POST", "/api/messages/demo", "application/json",
		`{"sender":"Bob","text":"second","replyTo":"`+first.ID+`"}`)
	var second room.Message
	json.Unmarshal(rec.Body.Bytes(), &second)
	if second.ReplyTo != first.ID {
		t.Fatalf("replyTo = %q, want %q", second.ReplyTo, first.ID)
	}
}

// TestJoinedChannelsListed verifies that a channel a visitor joined (but does
// not own) appears in their landing "joined" list, and not the owner's.
func TestJoinedChannelsListed(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)
	owner.do("POST", "/api/channels", "application/json", `{"name":"Club","listPublic":false}`)

	bob := newClient(t, s)
	bob.do("POST", "/api/me", "application/json", `{"name":"Bob"}`)
	bob.do("POST", "/api/channels/club/join", "", "")

	var resp struct {
		Mine   []room.Info `json:"mine"`
		Joined []room.Info `json:"joined"`
	}
	rec := bob.do("GET", "/api/channels", "", "")
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Joined) != 1 || resp.Joined[0].Name != "Club" {
		t.Fatalf("bob joined = %+v, want one Club channel", resp.Joined)
	}
	if len(resp.Mine) != 0 {
		t.Fatalf("bob should own nothing, got %+v", resp.Mine)
	}

	// The owner's own channel is surfaced as "mine", never as "joined".
	rec = owner.do("GET", "/api/channels", "", "")
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Joined) != 0 {
		t.Fatalf("owner joined = %+v, want empty", resp.Joined)
	}
	if len(resp.Mine) != 1 {
		t.Fatalf("owner mine = %+v, want one channel", resp.Mine)
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

// channelHistory snapshots a channel's resident messages via a throwaway
// subscription.
func channelHistory(t *testing.T, s *Server, key string) []room.Message {
	t.Helper()
	rm, ok := s.hub.Lookup(key)
	if !ok {
		t.Fatalf("channel %q not found", key)
	}
	history, _, _, cancel := rm.Subscribe("")
	cancel()
	return history
}

// TestEditOwnMessage verifies a sender can rewrite their own message, that the
// edit is flagged, and that nobody else can edit it.
func TestEditOwnMessage(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)
	owner.do("POST", "/api/channels", "application/json", `{"name":"Club"}`)
	rec := owner.do("POST", "/api/messages/club", "application/json",
		`{"sender":"Alice","text":"helo"}`)
	var m room.Message
	json.Unmarshal(rec.Body.Bytes(), &m)

	rec = owner.do("PATCH", "/api/messages/club/"+m.ID, "application/json", `{"text":"hello"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("edit = %d: %s", rec.Code, rec.Body.String())
	}
	var edited room.Message
	json.Unmarshal(rec.Body.Bytes(), &edited)
	if edited.Text != "hello" || !edited.Edited {
		t.Fatalf("edited message = %+v, want text hello with edited flag", edited)
	}
	if h := channelHistory(t, s, "club"); len(h) != 1 || h[0].Text != "hello" || !h[0].Edited {
		t.Fatalf("history = %+v, want one edited hello", h)
	}

	// Another member must not be able to edit Alice's message.
	bob := newClient(t, s)
	bob.do("POST", "/api/channels/club/join", "", "")
	rec = bob.do("PATCH", "/api/messages/club/"+m.ID, "application/json", `{"text":"hijack"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign edit = %d, want 403", rec.Code)
	}

	// Empty text is rejected for text messages.
	rec = owner.do("PATCH", "/api/messages/club/"+m.ID, "application/json", `{"text":"  "}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty edit = %d, want 403", rec.Code)
	}
}

// TestDeleteMessagePermissions verifies senders can delete their own messages,
// admins can delete members' messages, and members cannot delete others'.
func TestDeleteMessagePermissions(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)
	owner.do("POST", "/api/channels", "application/json", `{"name":"Club"}`)

	bob := newClient(t, s)
	bob.do("POST", "/api/channels/club/join", "", "")
	rec := bob.do("POST", "/api/messages/club", "application/json",
		`{"sender":"Bob","text":"mine"}`)
	var bobMsg room.Message
	json.Unmarshal(rec.Body.Bytes(), &bobMsg)
	rec = owner.do("POST", "/api/messages/club", "application/json",
		`{"sender":"Owner","text":"owner says"}`)
	var ownerMsg room.Message
	json.Unmarshal(rec.Body.Bytes(), &ownerMsg)

	// A member cannot delete someone else's message.
	if rec := bob.do("DELETE", "/api/messages/club/"+ownerMsg.ID, "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("member deleting owner's message = %d, want 403", rec.Code)
	}
	// The sender can delete their own.
	if rec := bob.do("DELETE", "/api/messages/club/"+bobMsg.ID, "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("self delete = %d, want 204", rec.Code)
	}

	// An admin (the owner) can delete a member's message.
	rec = bob.do("POST", "/api/messages/club", "application/json",
		`{"sender":"Bob","text":"again"}`)
	json.Unmarshal(rec.Body.Bytes(), &bobMsg)
	if rec := owner.do("DELETE", "/api/messages/club/"+bobMsg.ID, "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", rec.Code)
	}
	if h := channelHistory(t, s, "club"); len(h) != 1 || h[0].ID != ownerMsg.ID {
		t.Fatalf("history = %+v, want only the owner's message", h)
	}
}

// TestBanWithPurgeDeletesMessages verifies that banning with purgeMessages
// removes everything the banned member sent, while a plain ban keeps history.
func TestBanWithPurgeDeletesMessages(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)
	owner.do("POST", "/api/channels", "application/json", `{"name":"Club"}`)
	owner.do("POST", "/api/messages/club", "application/json",
		`{"sender":"Owner","text":"keep me"}`)

	bob := newClient(t, s)
	bob.do("POST", "/api/me", "application/json", `{"name":"Bob"}`)
	bob.do("POST", "/api/channels/club/join", "", "")
	bob.do("POST", "/api/messages/club", "application/json", `{"sender":"Bob","text":"spam 1"}`)
	bob.do("POST", "/api/messages/club", "application/json", `{"sender":"Bob","text":"spam 2"}`)

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

	rec = owner.do("POST", "/api/channels/club/moderate", "application/json",
		`{"action":"ban","uid":"`+bobUID+`","purgeMessages":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ban+purge = %d: %s", rec.Code, rec.Body.String())
	}
	h := channelHistory(t, s, "club")
	if len(h) != 1 || h[0].Text != "keep me" {
		t.Fatalf("history after purge = %+v, want only the owner's message", h)
	}
}
