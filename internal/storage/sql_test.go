package storage

import (
	"path/filepath"
	"testing"
	"time"
)

// newSQLite opens a fresh, isolated SQLite store backed by a temp file.
func newSQLite(t *testing.T) Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "nekodrop.db")
	st, err := Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSQLiteUserRoundTrip(t *testing.T) {
	st := newSQLite(t)
	if err := st.SaveUser(User{Token: "tok", UID: "100", Name: "Alice", Named: true}); err != nil {
		t.Fatalf("save user: %v", err)
	}
	// Overwrite the same UID; the latest write must win.
	if err := st.SaveUser(User{Token: "tok", UID: "100", Name: "Alice2", Named: true}); err != nil {
		t.Fatalf("resave user: %v", err)
	}
	users, err := st.LoadUsers()
	if err != nil {
		t.Fatalf("load users: %v", err)
	}
	if len(users) != 1 || users[0].Name != "Alice2" || !users[0].Named {
		t.Fatalf("unexpected users: %+v", users)
	}
}

func TestSQLiteMigrateCodeRoundTrip(t *testing.T) {
	st := newSQLite(t)
	if err := st.SaveUser(User{Token: "tok", UID: "100", Name: "Alice", Named: true, MigrateCode: "code-1"}); err != nil {
		t.Fatalf("save user: %v", err)
	}
	users, err := st.LoadUsers()
	if err != nil || len(users) != 1 || users[0].MigrateCode != "code-1" {
		t.Fatalf("migrate code not persisted: %+v err=%v", users, err)
	}
	// Disabling migration clears the code.
	if err := st.SaveUser(User{Token: "tok", UID: "100", Name: "Alice", Named: true}); err != nil {
		t.Fatalf("resave user: %v", err)
	}
	users, _ = st.LoadUsers()
	if len(users) != 1 || users[0].MigrateCode != "" {
		t.Fatalf("migrate code not cleared: %+v", users)
	}
}

func TestSQLiteKVRoundTrip(t *testing.T) {
	st := newSQLite(t)
	if _, ok, err := st.LoadKV("admin_state"); err != nil || ok {
		t.Fatalf("missing key: ok=%v err=%v", ok, err)
	}
	if err := st.SaveKV("admin_state", `{"a":1}`); err != nil {
		t.Fatalf("save kv: %v", err)
	}
	if err := st.SaveKV("admin_state", `{"a":2}`); err != nil {
		t.Fatalf("overwrite kv: %v", err)
	}
	v, ok, err := st.LoadKV("admin_state")
	if err != nil || !ok || v != `{"a":2}` {
		t.Fatalf("load kv = %q ok=%v err=%v", v, ok, err)
	}
}

func TestSQLiteStats(t *testing.T) {
	st := newSQLite(t)

	stats, err := st.Stats()
	if err != nil {
		t.Fatalf("stats on empty db: %v", err)
	}
	if !stats.Persistent {
		t.Fatal("sqlite stats must report Persistent")
	}
	if stats.Users != 0 || stats.Channels != 0 || stats.Messages != 0 || stats.Files != 0 || stats.FileBytes != 0 {
		t.Fatalf("empty db stats = %+v, want zero counts", stats)
	}

	if err := st.SaveUser(User{Token: "tok", UID: "100", Name: "Alice", Named: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveChannel(Channel{ID: "c1", Key: "demo"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(Message{ID: "m1", ChannelID: "c1", Kind: "text", Text: "hello", Time: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFile(File{ID: "f1", ChannelID: "c1", Name: "a.bin", Data: []byte("12345")}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAnnouncement(Announcement{ID: "a1", ChannelID: "c1", Text: "notice", Time: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	stats, err = st.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Users != 1 || stats.Channels != 1 || stats.Messages != 1 || stats.Files != 1 || stats.Announcements != 1 {
		t.Fatalf("counts = %+v, want one of each", stats)
	}
	if stats.FileBytes != 5 {
		t.Fatalf("FileBytes = %d, want 5", stats.FileBytes)
	}
	if stats.SizeBytes <= 0 {
		t.Fatalf("SizeBytes = %d, want > 0", stats.SizeBytes)
	}
}

func TestMemoryStatsNotPersistent(t *testing.T) {
	stats, err := NewMemory().Stats()
	if err != nil || stats.Persistent {
		t.Fatalf("memory stats = %+v err=%v, want non-persistent zeroes", stats, err)
	}
}

func TestSQLiteChannelAndHistoryRoundTrip(t *testing.T) {
	st := newSQLite(t)

	ch := Channel{
		ID: "abc123", Key: "team-cats", OwnerUID: "100",
		Name: "Team Cats", Description: "meow", Visibility: "public",
		ListPublic: true, AllowJoin: true, AllowSpeak: true,
		Admins: []string{"100"}, Members: []string{"100", "200"},
		Muted: []string{"200"}, Names: map[string]string{"100": "Alice", "200": "Bob"},
		Nicks:   map[string]string{"200": "Bobby"},
		Pending: map[string]string{"300": "Carol"},
	}
	if err := st.SaveChannel(ch); err != nil {
		t.Fatalf("save channel: %v", err)
	}
	if err := st.AppendMessage(Message{
		ID: "m1", ChannelID: "abc123", Kind: "text", Sender: "Alice", SenderUID: "100",
		Text: "hello", Mentions: []string{"200"}, Time: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append message: %v", err)
	}
	if err := st.AppendMessage(Message{
		ID: "m2", ChannelID: "abc123", Kind: "text", Sender: "Bob", SenderUID: "200",
		Text: "re: hello", ReplyTo: "m1", Time: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append reply: %v", err)
	}
	if err := st.SaveFile(File{ID: "f1", ChannelID: "abc123", Name: "n.txt", ContentType: "text/plain", Data: []byte("data")}); err != nil {
		t.Fatalf("save file: %v", err)
	}
	if err := st.AppendAnnouncement(Announcement{ID: "a1", ChannelID: "abc123", AuthorUID: "100", AuthorName: "Alice", Text: "notice", Time: time.Now().UTC()}); err != nil {
		t.Fatalf("append announcement: %v", err)
	}

	chans, err := st.LoadChannels()
	if err != nil || len(chans) != 1 {
		t.Fatalf("load channels = %+v, err=%v", chans, err)
	}
	got := chans[0]
	if got.Name != "Team Cats" || len(got.Members) != 2 || got.Names["200"] != "Bob" || got.Pending["300"] != "Carol" {
		t.Fatalf("unexpected channel: %+v", got)
	}
	if got.Nicks["200"] != "Bobby" {
		t.Fatalf("nick not persisted: %+v", got.Nicks)
	}

	msgs, err := st.LoadMessages("abc123")
	if err != nil || len(msgs) != 2 || msgs[0].Text != "hello" || len(msgs[0].Mentions) != 1 {
		t.Fatalf("unexpected messages: %+v err=%v", msgs, err)
	}
	if msgs[1].ReplyTo != "m1" {
		t.Fatalf("reply_to not persisted: %+v", msgs[1])
	}

	f, ok, err := st.LoadFile("f1")
	if err != nil || !ok || string(f.Data) != "data" {
		t.Fatalf("load file: ok=%v err=%v f=%+v", ok, err, f)
	}
	if _, ok, _ := st.LoadFile("missing"); ok {
		t.Fatal("missing file should not be found")
	}

	anns, err := st.LoadAnnouncements("abc123")
	if err != nil || len(anns) != 1 || anns[0].Text != "notice" {
		t.Fatalf("unexpected announcements: %+v err=%v", anns, err)
	}

	if err := st.DeleteChannel("abc123"); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	chans, _ = st.LoadChannels()
	if len(chans) != 0 {
		t.Fatalf("channel not deleted: %+v", chans)
	}
}

// TestSQLiteMessageDeletion verifies single-message deletion, per-sender
// purging, and that purging sweeps the file payloads those messages owned.
func TestSQLiteMessageDeletion(t *testing.T) {
	st := newSQLite(t)
	now := time.Now().UTC().Truncate(time.Second)
	msgs := []Message{
		{ID: "m1", ChannelID: "ch", Kind: "text", Sender: "Bob", SenderUID: "1", Text: "a", Time: now},
		{ID: "m2", ChannelID: "ch", Kind: "file", Sender: "Bob", SenderUID: "1", FileID: "f1", FileName: "x.bin", Time: now},
		{ID: "m3", ChannelID: "ch", Kind: "text", Sender: "Eve", SenderUID: "2", Text: "b", Edited: true, Time: now},
	}
	for _, m := range msgs {
		if err := st.AppendMessage(m); err != nil {
			t.Fatalf("append %s: %v", m.ID, err)
		}
	}
	if err := st.SaveFile(File{ID: "f1", ChannelID: "ch", Name: "x.bin", Data: []byte{1}}); err != nil {
		t.Fatalf("save file: %v", err)
	}

	if err := st.DeleteMessage("m3"); err != nil {
		t.Fatalf("delete message: %v", err)
	}
	if err := st.DeleteMessagesBySender("ch", "1"); err != nil {
		t.Fatalf("purge sender: %v", err)
	}
	got, err := st.LoadMessages("ch")
	if err != nil {
		t.Fatalf("load messages: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("messages after deletion = %+v, want none", got)
	}
	if _, found, err := st.LoadFile("f1"); err != nil || found {
		t.Fatalf("purged file still present (found=%v err=%v)", found, err)
	}
}

// TestSQLiteEditedFlagRoundTrip verifies the edited flag survives persistence.
func TestSQLiteEditedFlagRoundTrip(t *testing.T) {
	st := newSQLite(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.AppendMessage(Message{ID: "m1", ChannelID: "ch", Kind: "text", SenderUID: "1", Text: "v1", Time: now}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// An edit re-appends the same ID with new text; REPLACE must overwrite.
	if err := st.AppendMessage(Message{ID: "m1", ChannelID: "ch", Kind: "text", SenderUID: "1", Text: "v2", Edited: true, Time: now}); err != nil {
		t.Fatalf("re-append: %v", err)
	}
	got, err := st.LoadMessages("ch")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].Text != "v2" || !got[0].Edited {
		t.Fatalf("messages = %+v, want one edited v2", got)
	}
}
