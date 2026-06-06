package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moehoshio/NekoDrop/internal/room"
)

// cookieClient drives the server while carrying the identity cookie between
// requests, the way a browser would.
type cookieClient struct {
	t      *testing.T
	s      *Server
	cookie *http.Cookie
}

func newClient(t *testing.T, s *Server) *cookieClient {
	return &cookieClient{t: t, s: s}
}

func (c *cookieClient) do(method, path, ctype, body string) *httptest.ResponseRecorder {
	c.t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if ctype != "" {
		r.Header.Set("Content-Type", ctype)
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	c.s.ServeHTTP(rec, r)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == cookieName {
			c.cookie = ck
		}
	}
	return rec
}

func TestMentionParsing(t *testing.T) {
	got := parseMentions("hi @alice#101 and @bob#202, also @alice#101 again and bad@x")
	want := []string{"101", "202"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMentions = %v, want %v", got, want)
	}
	if parseMentions("no mentions here") != nil {
		t.Fatal("expected nil for text without mentions")
	}
}

func TestIdentityIsStableAcrossRequests(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	rec := c.do("GET", "/api/me", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me = %d", rec.Code)
	}
	var u1 struct{ UID, Name string }
	json.Unmarshal(rec.Body.Bytes(), &u1)
	if u1.UID == "" {
		t.Fatal("expected a UID")
	}

	// Rename, then confirm the UID is unchanged.
	rec = c.do("POST", "/api/me", "application/json", `{"name":"Alice"}`)
	var u2 struct{ UID, Name string }
	json.Unmarshal(rec.Body.Bytes(), &u2)
	if u2.UID != u1.UID {
		t.Fatalf("UID changed across rename: %s -> %s", u1.UID, u2.UID)
	}
	if u2.Name != "Alice" {
		t.Fatalf("name = %q, want Alice", u2.Name)
	}
}

func TestCreateAndListPublicChannel(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	rec := c.do("POST", "/api/channels", "application/json",
		`{"name":"Team Cats","description":"meow","visibility":"public","listPublic":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d: %s", rec.Code, rec.Body.String())
	}
	var info room.Info
	json.Unmarshal(rec.Body.Bytes(), &info)
	if info.Key != "team-cats" || info.ID == "" {
		t.Fatalf("unexpected channel info: %+v", info)
	}

	rec = c.do("GET", "/api/channels", "", "")
	var list struct {
		Channels []room.Info `json:"channels"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Channels) != 1 || list.Channels[0].Name != "Team Cats" {
		t.Fatalf("public list = %+v", list.Channels)
	}
}

func TestChannelCreationLimit(t *testing.T) {
	s, err := New(Options{MaxUploadBytes: 1 << 20, MaxChannelsPerUser: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := newClient(t, s)
	if rec := c.do("POST", "/api/channels", "application/json", `{"name":"one"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first create = %d", rec.Code)
	}
	if rec := c.do("POST", "/api/channels", "application/json", `{"name":"two"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("over-limit create = %d, want 403", rec.Code)
	}
}

func TestPrivateChannelStreamRequiresMembership(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)
	if rec := owner.do("POST", "/api/channels", "application/json",
		`{"name":"vault","visibility":"private"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create private = %d: %s", rec.Code, rec.Body.String())
	}

	// A different visitor cannot stream the private channel.
	outsider := newClient(t, s)
	if rec := outsider.do("GET", "/api/stream/vault", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("outsider stream = %d, want 403", rec.Code)
	}

	// After joining, the outsider gains access.
	if rec := outsider.do("POST", "/api/channels/vault/join", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("join = %d: %s", rec.Code, rec.Body.String())
	}
	// The stream handler blocks until the request context is cancelled, so use
	// a short-lived context to capture the initial response.
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", "/api/stream/vault", nil).WithContext(ctx)
	r.AddCookie(outsider.cookie)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("member stream = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("member stream content-type = %q", ct)
	}
}

func TestDissolveReleasesPath(t *testing.T) {
	s := newTestServer(t)
	owner := newClient(t, s)
	owner.do("POST", "/api/channels", "application/json", `{"name":"temp"}`)

	// A non-owner cannot dissolve.
	other := newClient(t, s)
	if rec := other.do("DELETE", "/api/channels/temp", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner dissolve = %d, want 403", rec.Code)
	}
	// The owner can.
	if rec := owner.do("DELETE", "/api/channels/temp", "", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("owner dissolve = %d, want 204", rec.Code)
	}
}
