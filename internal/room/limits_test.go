package room

import (
	"strconv"
	"testing"
)

// TestMessageHistoryIsBounded verifies the per-channel message cap evicts the
// oldest messages instead of growing without bound.
func TestMessageHistoryIsBounded(t *testing.T) {
	r := newRoom("test", "id1")
	r.applyLimits(Limits{MaxMessagesPerChannel: 5, MaxFileBytesPerChannel: 1 << 20, MaxSubscribersPerChannel: 10, MaxChannels: 10})

	for i := 0; i < 50; i++ {
		r.AddText("alice", "100", "m"+strconv.Itoa(i), nil, false)
	}
	r.mu.RLock()
	n := len(r.messages)
	first := r.messages[0].Text
	last := r.messages[len(r.messages)-1].Text
	r.mu.RUnlock()
	if n != 5 {
		t.Fatalf("history len = %d, want 5", n)
	}
	if first != "m45" || last != "m49" {
		t.Fatalf("history window = [%s..%s], want [m45..m49]", first, last)
	}
}

// TestFileBytesAreBounded verifies that uploaded payloads are evicted once the
// per-channel byte budget is exceeded, freeing memory.
func TestFileBytesAreBounded(t *testing.T) {
	r := newRoom("test", "id1")
	// Budget for ~3 KiB; each upload is 1 KiB.
	r.applyLimits(Limits{MaxMessagesPerChannel: 1000, MaxFileBytesPerChannel: 3 * 1024, MaxSubscribersPerChannel: 10, MaxChannels: 10})

	payload := make([]byte, 1024)
	var ids []string
	for i := 0; i < 10; i++ {
		m := r.AddFile("bob", "101", "f"+strconv.Itoa(i)+".bin", "application/octet-stream", payload)
		ids = append(ids, m.FileID)
	}

	r.mu.RLock()
	bytes := r.fileBytes
	resident := len(r.files)
	r.mu.RUnlock()
	if bytes > 3*1024 {
		t.Fatalf("fileBytes = %d, want <= 3072", bytes)
	}
	if resident > 3 {
		t.Fatalf("resident files = %d, want <= 3", resident)
	}
	// The earliest files were evicted; the most recent remain downloadable.
	if _, err := r.File(ids[0]); err == nil {
		t.Fatal("oldest file should have been evicted")
	}
	if _, err := r.File(ids[len(ids)-1]); err != nil {
		t.Fatalf("newest file should remain: %v", err)
	}
}

// TestRosterMapNotGrownByAnonymousSenders verifies that transient speakers with
// no channel standing do not accumulate in the roster map, so a flood of fresh
// identities posting to one channel cannot grow memory without bound.
func TestRosterMapNotGrownByAnonymousSenders(t *testing.T) {
	r := newRoom("open", "id1") // ownerless: speakers are not members
	for i := 0; i < 1000; i++ {
		uid := strconv.Itoa(1000 + i)
		r.AddText("flood", uid, "spam", nil, false)
	}
	r.mu.RLock()
	n := len(r.names)
	r.mu.RUnlock()
	if n != 0 {
		t.Fatalf("roster map = %d entries, want 0 for standing-less speakers", n)
	}

	// A genuine member's name is still tracked.
	r.members["100"] = true
	r.AddText("alice", "100", "hi", nil, false)
	r.mu.RLock()
	_, ok := r.names["100"]
	r.mu.RUnlock()
	if !ok {
		t.Fatal("a member's name should be tracked in the roster")
	}
}

// TestLiveChannelCountIsBounded verifies the hub reclaims idle, ownerless
// channels when the channel cap is reached.
func TestLiveChannelCountIsBounded(t *testing.T) {
	h := NewHubWithStore(0, nil, Limits{MaxChannels: 3})

	// Spawn many ad-hoc rooms by hitting unique keys, as anonymous traffic can.
	for i := 0; i < 100; i++ {
		h.Room("adhoc-" + strconv.Itoa(i))
	}
	if got := h.Len(); got > 3 {
		t.Fatalf("live channels = %d, want <= 3", got)
	}
}

// TestActiveAndOwnedChannelsSurviveReclaim verifies reclamation never evicts an
// owned channel or one with live subscribers.
func TestActiveAndOwnedChannelsSurviveReclaim(t *testing.T) {
	h := NewHubWithStore(0, nil, Limits{MaxChannels: 3})

	owned, err := h.Create("kept", "100", Settings{})
	if err != nil {
		t.Fatal(err)
	}
	// An ad-hoc room with a live subscriber must also be protected.
	active := h.Room("busy")
	_, _, _, cancel := active.Subscribe("200")
	defer cancel()

	for i := 0; i < 100; i++ {
		h.Room("flood-" + strconv.Itoa(i))
	}

	if _, ok := h.Lookup("kept"); !ok {
		t.Fatal("owned channel must not be reclaimed")
	}
	if _, ok := h.Lookup("busy"); !ok {
		t.Fatal("channel with a live subscriber must not be reclaimed")
	}
	_ = owned
}
