package room

import (
	"testing"
	"time"
)

// drainEvent waits for the next event of the given kind on ch.
func drainEvent(t *testing.T, ch <-chan Event, kind EventKind) Event {
	t.Helper()
	timeout := time.After(time.Second)
	for {
		select {
		case e := <-ch:
			if e.Type == kind {
				return e
			}
		case <-timeout:
			t.Fatalf("did not receive %s event", kind)
		}
	}
}

// TestApplyNickResolvesAndBroadcasts verifies that a per-channel nickname
// overrides the global name for a member, that it is retroactively reflected by
// DisplayName, and that changing it broadcasts an identity event. Messages stay
// keyed by UID throughout.
func TestApplyNickResolvesAndBroadcasts(t *testing.T) {
	r := newRoom("test", "id1")
	r.members["100"] = true // give the user a standing so the nick is retained

	_, _, ch, cancel := r.Subscribe("100")
	defer cancel()

	// No nick yet: the global name is used.
	if got := r.ApplyNick("100", "Alice", ""); got != "Alice" {
		t.Fatalf("ApplyNick empty = %q, want Alice", got)
	}

	// Setting a nick resolves to it and broadcasts an identity event.
	if got := r.ApplyNick("100", "Alice", "Ally"); got != "Ally" {
		t.Fatalf("ApplyNick = %q, want Ally", got)
	}
	ev := drainEvent(t, ch, EventIdentity)
	if ev.Identity == nil || ev.Identity.UID != "100" || ev.Identity.Name != "Ally" {
		t.Fatalf("identity event = %+v, want {100 Ally}", ev.Identity)
	}

	// DisplayName re-resolves a message's stored name against the current nick,
	// so history replay reflects the change.
	if got := r.DisplayName("100", "Alice"); got != "Ally" {
		t.Fatalf("DisplayName = %q, want Ally", got)
	}
	// A UID with no nick falls back to the supplied name.
	if got := r.DisplayName("999", "Zed"); got != "Zed" {
		t.Fatalf("DisplayName fallback = %q, want Zed", got)
	}
}

// TestAnnouncementLifecycle covers posting, editing and removing announcements:
// multiple coexist, only owners/admins may mutate them, and each mutation is
// broadcast to live subscribers.
func TestAnnouncementLifecycle(t *testing.T) {
	r := newRoom("test", "id1")
	r.ownerUID = "100" // the owner may manage announcements

	_, anns, ch, cancel := r.Subscribe("100")
	defer cancel()
	if len(anns) != 0 {
		t.Fatalf("expected no announcements initially, got %d", len(anns))
	}

	a1 := r.AddAnnouncement("100", "owner", "first")
	a2 := r.AddAnnouncement("100", "owner", "second")
	drainEvent(t, ch, EventAnnouncement)
	drainEvent(t, ch, EventAnnouncement)
	if got := r.Announcements(); len(got) != 2 {
		t.Fatalf("expected 2 coexisting announcements, got %d", len(got))
	}

	// A non-admin cannot edit or delete.
	if _, err := r.EditAnnouncement("200", a1.ID, "nope"); err == nil {
		t.Fatal("non-admin edit should be refused")
	}
	if err := r.DeleteAnnouncement("200", a1.ID); err == nil {
		t.Fatal("non-admin delete should be refused")
	}

	// The owner edits the first announcement.
	edited, err := r.EditAnnouncement("100", a1.ID, "first (updated)")
	if err != nil {
		t.Fatalf("EditAnnouncement: %v", err)
	}
	if !edited.Edited || edited.Text != "first (updated)" || edited.ID != a1.ID {
		t.Fatalf("unexpected edited announcement: %+v", edited)
	}
	ev := drainEvent(t, ch, EventAnnouncementEdited)
	if ev.Announcement == nil || ev.Announcement.Text != "first (updated)" || !ev.Announcement.Edited {
		t.Fatalf("edited event = %+v", ev.Announcement)
	}

	// The owner deletes the second announcement.
	if err := r.DeleteAnnouncement("100", a2.ID); err != nil {
		t.Fatalf("DeleteAnnouncement: %v", err)
	}
	del := drainEvent(t, ch, EventAnnouncementDeleted)
	if del.AnnouncementID != a2.ID {
		t.Fatalf("deleted event id = %q, want %q", del.AnnouncementID, a2.ID)
	}
	got := r.Announcements()
	if len(got) != 1 || got[0].ID != a1.ID || !got[0].Edited {
		t.Fatalf("after delete, announcements = %+v", got)
	}

	// Editing or deleting a missing announcement reports not-found.
	if _, err := r.EditAnnouncement("100", "missing", "x"); err != ErrAnnouncementNotFound {
		t.Fatalf("edit missing err = %v, want ErrAnnouncementNotFound", err)
	}
	if err := r.DeleteAnnouncement("100", "missing"); err != ErrAnnouncementNotFound {
		t.Fatalf("delete missing err = %v, want ErrAnnouncementNotFound", err)
	}
}

// drainMessage waits for the next message event on ch, ignoring presence and
// other non-message events that may be interleaved.
func drainMessage(t *testing.T, ch <-chan Event) Message {
	t.Helper()
	timeout := time.After(time.Second)
	for {
		select {
		case e := <-ch:
			if e.Type == EventMessage && e.Message != nil {
				return *e.Message
			}
		case <-timeout:
			t.Fatal("did not receive message broadcast")
		}
	}
}

func TestAddTextBroadcastsAndStoresHistory(t *testing.T) {
	r := newRoom("test", "id1")

	history, _, ch, cancel := r.Subscribe("100")
	defer cancel()
	if len(history) != 0 {
		t.Fatalf("expected empty history, got %d", len(history))
	}

	sent := r.AddText("alice", "100", "hello", nil, false, "")
	if sent.Kind != KindText || sent.Text != "hello" || sent.Sender != "alice" || sent.SenderUID != "100" {
		t.Fatalf("unexpected message: %+v", sent)
	}

	got := drainMessage(t, ch)
	if got.ID != sent.ID || got.Text != "hello" {
		t.Fatalf("received wrong message: %+v", got)
	}

	// A new subscriber must see the prior message in history.
	history2, _, _, cancel2 := r.Subscribe("101")
	defer cancel2()
	if len(history2) != 1 || history2[0].Text != "hello" {
		t.Fatalf("expected history with 1 message, got %+v", history2)
	}
}

func TestAddFileStoresPayload(t *testing.T) {
	r := newRoom("test", "id1")
	data := []byte("file-bytes")

	m := r.AddFile("bob", "101", "notes.txt", "text/plain", data, "", nil, false, "")
	if m.Kind != KindFile || m.FileName != "notes.txt" || m.FileSize != int64(len(data)) {
		t.Fatalf("unexpected file message: %+v", m)
	}

	f, err := r.File(m.FileID)
	if err != nil {
		t.Fatalf("file not found: %v", err)
	}
	if string(f.Data) != "file-bytes" || f.ContentType != "text/plain" {
		t.Fatalf("unexpected stored file: %+v", f)
	}

	if _, err := r.File("missing"); err != ErrFileNotFound {
		t.Fatalf("expected ErrFileNotFound, got %v", err)
	}
}

func TestCancelRemovesSubscriber(t *testing.T) {
	r := newRoom("test", "id1")
	_, _, _, cancel := r.Subscribe("100")
	if r.Subscribers() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", r.Subscribers())
	}
	if r.OnlineCount() != 1 {
		t.Fatalf("expected 1 online, got %d", r.OnlineCount())
	}
	cancel()
	if r.Subscribers() != 0 {
		t.Fatalf("expected 0 subscribers after cancel, got %d", r.Subscribers())
	}
	if r.OnlineCount() != 0 {
		t.Fatalf("expected 0 online after cancel, got %d", r.OnlineCount())
	}
	cancel() // second cancel must be a no-op and not panic
}

func TestPublishDoesNotBlockOnFullSubscriber(t *testing.T) {
	r := newRoom("test", "id1")
	_, _, _, cancel := r.Subscribe("100")
	defer cancel()

	// Fill the buffer plus extra; publish must never block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			r.AddText("x", "100", "msg", nil, false, "")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
}

func TestHubReturnsSameRoom(t *testing.T) {
	h := NewHub(0)
	a := h.Room("alpha")
	b := h.Room("alpha")
	if a != b {
		t.Fatal("expected the same room instance for the same key")
	}
	if h.Room("beta") == a {
		t.Fatal("expected different rooms for different keys")
	}
	if h.Len() != 2 {
		t.Fatalf("expected 2 rooms, got %d", h.Len())
	}
}

func TestPrivateChannelAccessControl(t *testing.T) {
	h := NewHub(0)
	rm, err := h.Create("secret", "100", Settings{Name: "Secret", Visibility: Private, AllowJoin: true})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rm.CanRead("999") {
		t.Fatal("non-member should not read a private channel")
	}
	if !rm.CanRead("100") {
		t.Fatal("owner should read their private channel")
	}
	if pending, err := rm.Join("200", "bob"); err != nil || pending {
		t.Fatalf("join: pending=%v err=%v", pending, err)
	}
	if !rm.CanRead("200") {
		t.Fatal("member should read after joining")
	}
}

func TestModerationAndOwnership(t *testing.T) {
	h := NewHub(0)
	rm, _ := h.Create("club", "100", Settings{Visibility: Public, AllowJoin: true, AllowSpeak: true})
	if _, err := rm.Join("200", "bob"); err != nil {
		t.Fatal(err)
	}
	// Owner promotes bob to admin.
	if err := rm.Moderate("100", "promote", "200"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !rm.IsAdmin("200") {
		t.Fatal("bob should be admin after promotion")
	}
	// A muted user cannot speak.
	rm.Join("300", "carol")
	rm.Moderate("100", "mute", "300")
	if rm.CanSpeak("300") {
		t.Fatal("muted user should not speak")
	}
	// The owner cannot be moderated.
	if err := rm.Moderate("200", "ban", "100"); err == nil {
		t.Fatal("owner must not be a moderation target")
	}
}

func TestCreateLimitAndDissolve(t *testing.T) {
	h := NewHub(1)
	if _, err := h.Create("one", "100", Settings{}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := h.Create("two", "100", Settings{}); err != ErrLimitReached {
		t.Fatalf("expected ErrLimitReached, got %v", err)
	}
	first, _ := h.Lookup("one")
	id := first.ID
	if err := h.Dissolve("one", "100"); err != nil {
		t.Fatalf("dissolve: %v", err)
	}
	if _, ok := h.Lookup("one"); ok {
		t.Fatal("key should be released after dissolve")
	}
	// Quota freed: the user can create again, and the freed key is reusable.
	reborn, err := h.Create("one", "100", Settings{})
	if err != nil {
		t.Fatalf("recreate after dissolve: %v", err)
	}
	if reborn.ID == id {
		t.Fatal("a retired channel ID must never be reissued")
	}
}

// TestHubStats verifies the aggregate dashboard snapshot across channels.
func TestHubStats(t *testing.T) {
	h := NewHub(0)
	r1 := h.Room("one")
	r1.AddText("alice", "100", "hello", nil, false, "")
	r1.AddText("bob", "200", "world!", nil, false, "")
	r1.AddFile("alice", "100", "a.bin", "application/octet-stream", []byte{1, 2, 3, 4}, "", nil, false, "")
	r2 := h.Room("two")
	r2.AddAnnouncement("100", "alice", "notice")
	_, _, _, cancel := r2.Subscribe("100")
	defer cancel()

	st := h.Stats()
	if st.Channels != 2 {
		t.Fatalf("Channels = %d, want 2", st.Channels)
	}
	if st.Messages != 3 || st.Files != 1 || st.Announcements != 1 {
		t.Fatalf("Messages/Files/Announcements = %d/%d/%d, want 3/1/1", st.Messages, st.Files, st.Announcements)
	}
	if want := int64(len("hello") + len("world!")); st.TextBytes != want {
		t.Fatalf("TextBytes = %d, want %d", st.TextBytes, want)
	}
	if st.FileBytes != 4 {
		t.Fatalf("FileBytes = %d, want 4", st.FileBytes)
	}
	if st.Subscribers != 1 || st.Online != 1 {
		t.Fatalf("Subscribers/Online = %d/%d, want 1/1", st.Subscribers, st.Online)
	}
}
