package room

import (
	"testing"
	"time"
)

func TestAddTextBroadcastsAndStoresHistory(t *testing.T) {
	r := newRoom("test")

	history, ch, cancel := r.Subscribe()
	defer cancel()
	if len(history) != 0 {
		t.Fatalf("expected empty history, got %d", len(history))
	}

	sent := r.AddText("alice", "hello")
	if sent.Kind != KindText || sent.Text != "hello" || sent.Sender != "alice" {
		t.Fatalf("unexpected message: %+v", sent)
	}

	select {
	case got := <-ch:
		if got.ID != sent.ID || got.Text != "hello" {
			t.Fatalf("received wrong message: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive broadcast")
	}

	// A new subscriber must see the prior message in history.
	history2, _, cancel2 := r.Subscribe()
	defer cancel2()
	if len(history2) != 1 || history2[0].Text != "hello" {
		t.Fatalf("expected history with 1 message, got %+v", history2)
	}
}

func TestAddFileStoresPayload(t *testing.T) {
	r := newRoom("test")
	data := []byte("file-bytes")

	m := r.AddFile("bob", "notes.txt", "text/plain", data)
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
	r := newRoom("test")
	_, _, cancel := r.Subscribe()
	if r.Subscribers() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", r.Subscribers())
	}
	cancel()
	if r.Subscribers() != 0 {
		t.Fatalf("expected 0 subscribers after cancel, got %d", r.Subscribers())
	}
	cancel() // second cancel must be a no-op and not panic
}

func TestPublishDoesNotBlockOnFullSubscriber(t *testing.T) {
	r := newRoom("test")
	_, _, cancel := r.Subscribe()
	defer cancel()

	// Fill the buffer (cap 32) plus extra; publish must never block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			r.AddText("x", "msg")
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
	h := NewHub()
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
