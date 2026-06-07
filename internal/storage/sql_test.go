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

func TestSQLiteChannelAndHistoryRoundTrip(t *testing.T) {
	st := newSQLite(t)

	ch := Channel{
		ID: "abc123", Key: "team-cats", OwnerUID: "100",
		Name: "Team Cats", Description: "meow", Visibility: "public",
		ListPublic: true, AllowJoin: true, AllowSpeak: true,
		Admins: []string{"100"}, Members: []string{"100", "200"},
		Muted: []string{"200"}, Names: map[string]string{"100": "Alice", "200": "Bob"},
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

	msgs, err := st.LoadMessages("abc123")
	if err != nil || len(msgs) != 1 || msgs[0].Text != "hello" || len(msgs[0].Mentions) != 1 {
		t.Fatalf("unexpected messages: %+v err=%v", msgs, err)
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
