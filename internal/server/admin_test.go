package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moehoshio/NekoDrop/internal/room"
	"github.com/moehoshio/NekoDrop/internal/storage"
)

const testAdminToken = "sekrit-test-token"

func newAdminServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if opts.MaxUploadBytes == 0 {
		opts.MaxUploadBytes = 1 << 20
	}
	opts.AdminEnabled = true
	opts.AdminToken = testAdminToken
	s, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// adminDo performs an admin API request carrying the given token.
func adminDo(t *testing.T, s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

// uploadBytes uploads a payload of n bytes to a room as the given client.
func uploadBytes(t *testing.T, c *cookieClient, roomKey string, n int) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "payload.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(bytes.Repeat([]byte("x"), n)); err != nil {
		t.Fatal(err)
	}
	mw.Close()
	return c.do("POST", "/api/files/"+roomKey, mw.FormDataContentType(), buf.String())
}

func TestAdminDisabledByDefault(t *testing.T) {
	s := newTestServer(t)
	paths := []struct{ method, path string }{
		{"GET", "/admin"},
		{"GET", "/api/admin/overview"},
		{"GET", "/api/admin/channels"},
		{"GET", "/api/admin/users"},
		{"GET", "/api/admin/config"},
	}
	for _, p := range paths {
		if rec := adminDo(t, s, testAdminToken, p.method, p.path, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s with panel disabled = %d, want 404", p.method, p.path, rec.Code)
		}
	}
}

func TestAdminEnabledWithoutTokenStaysDisabled(t *testing.T) {
	s, err := New(Options{MaxUploadBytes: 1 << 20, AdminEnabled: true, AdminToken: ""})
	if err != nil {
		t.Fatal(err)
	}
	if rec := adminDo(t, s, "", "GET", "/admin", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /admin without configured token = %d, want 404", rec.Code)
	}
}

func TestAdminRequiresToken(t *testing.T) {
	s := newAdminServer(t, Options{})
	if rec := adminDo(t, s, "", "GET", "/api/admin/overview", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token = %d, want 401", rec.Code)
	}
	if rec := adminDo(t, s, "wrong", "GET", "/api/admin/overview", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", rec.Code)
	}
	rec := adminDo(t, s, testAdminToken, "GET", "/api/admin/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token = %d, want 200", rec.Code)
	}
	var ov struct {
		Backend string `json:"backend"`
	}
	json.Unmarshal(rec.Body.Bytes(), &ov)
	if ov.Backend != "memory" {
		t.Fatalf("overview backend = %q", ov.Backend)
	}
	// The panel page itself is served when enabled.
	if rec := adminDo(t, s, "", "GET", "/admin", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /admin = %d, want 200", rec.Code)
	}
}

func TestAdminBanUserBlocksWrites(t *testing.T) {
	s := newAdminServer(t, Options{})
	c := newClient(t, s)

	rec := c.do("GET", "/api/me", "", "")
	var me struct{ UID string }
	json.Unmarshal(rec.Body.Bytes(), &me)

	if rec := c.do("POST", "/api/messages/demo", "application/json", `{"text":"hi"}`); rec.Code != http.StatusCreated {
		t.Fatalf("pre-ban message = %d", rec.Code)
	}

	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/users/"+me.UID, `{"banned":true}`); rec.Code != http.StatusOK {
		t.Fatalf("ban user = %d: %s", rec.Code, rec.Body.String())
	}

	if rec := c.do("POST", "/api/messages/demo", "application/json", `{"text":"hi again"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("banned user message = %d, want 403", rec.Code)
	}
	if rec := c.do("POST", "/api/channels", "application/json", `{"name":"newchan"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("banned user create channel = %d, want 403", rec.Code)
	}
	if rec := c.do("POST", "/api/channels/demo/join", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("banned user join = %d, want 403", rec.Code)
	}
	if rec := uploadBytes(t, c, "demo", 8); rec.Code != http.StatusForbidden {
		t.Fatalf("banned user upload = %d, want 403", rec.Code)
	}

	// The roster reflects the ban, and unbanning restores access.
	rec = adminDo(t, s, testAdminToken, "GET", "/api/admin/users", "")
	if !strings.Contains(rec.Body.String(), `"banned":true`) {
		t.Fatalf("admin user list does not show the ban: %s", rec.Body.String())
	}
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/users/"+me.UID, `{"banned":false}`); rec.Code != http.StatusOK {
		t.Fatalf("unban user = %d", rec.Code)
	}
	if rec := c.do("POST", "/api/messages/demo", "application/json", `{"text":"back"}`); rec.Code != http.StatusCreated {
		t.Fatalf("post-unban message = %d", rec.Code)
	}
}

func TestAdminBanChannelDisablesIt(t *testing.T) {
	s := newAdminServer(t, Options{})
	c := newClient(t, s)

	if rec := c.do("POST", "/api/channels", "application/json",
		`{"name":"Doomed","listPublic":true}`); rec.Code != http.StatusCreated {
		t.Fatal("create channel failed")
	}
	rm, ok := s.hub.Lookup("doomed")
	if !ok {
		t.Fatal("channel not found in hub")
	}

	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/channels/"+rm.ID, `{"banned":true}`); rec.Code != http.StatusOK {
		t.Fatalf("ban channel = %d: %s", rec.Code, rec.Body.String())
	}

	if rec := c.do("POST", "/api/messages/doomed", "application/json", `{"text":"hi"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("message to disabled channel = %d, want 403", rec.Code)
	}
	if rec := c.do("GET", "/api/stream/doomed", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("stream of disabled channel = %d, want 403", rec.Code)
	}
	if rec := c.do("DELETE", "/api/channels/doomed", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("dissolve of disabled channel = %d, want 403", rec.Code)
	}

	// Disabled channels vanish from every user-facing listing.
	rec := c.do("GET", "/api/channels", "", "")
	var list struct {
		Channels []room.Info `json:"channels"`
		Mine     []room.Info `json:"mine"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Channels) != 0 || len(list.Mine) != 0 {
		t.Fatalf("disabled channel still listed: %+v", list)
	}

	// The channel view flags the state for the room page.
	rec = c.do("GET", "/api/channels/doomed", "", "")
	var view struct {
		Disabled bool `json:"disabled"`
	}
	json.Unmarshal(rec.Body.Bytes(), &view)
	if !view.Disabled {
		t.Fatalf("channel view not flagged disabled: %s", rec.Body.String())
	}

	// Unbanning restores the channel.
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/channels/"+rm.ID, `{"banned":false}`); rec.Code != http.StatusOK {
		t.Fatalf("unban channel = %d", rec.Code)
	}
	if rec := c.do("POST", "/api/messages/doomed", "application/json", `{"text":"back"}`); rec.Code != http.StatusCreated {
		t.Fatalf("message after unban = %d", rec.Code)
	}
}

func TestAdminUploadExemptions(t *testing.T) {
	s := newAdminServer(t, Options{MaxUploadBytes: 100})
	c := newClient(t, s)

	rec := c.do("GET", "/api/me", "", "")
	var me struct{ UID string }
	json.Unmarshal(rec.Body.Bytes(), &me)

	// Materialize the channel so it has an ID to attach an exemption to.
	c.do("POST", "/api/messages/demo", "application/json", `{"text":"hi"}`)
	rm, _ := s.hub.Lookup("demo")

	if rec := uploadBytes(t, c, "demo", 200); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload over base limit = %d, want 413", rec.Code)
	}

	// A per-channel exemption raises the limit for everyone in that channel.
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/channels/"+rm.ID, `{"maxUploadBytes":1024}`); rec.Code != http.StatusOK {
		t.Fatalf("set channel exemption = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := uploadBytes(t, c, "demo", 200); rec.Code != http.StatusCreated {
		t.Fatalf("upload within channel exemption = %d, want 201", rec.Code)
	}

	// A per-user override wins over the channel exemption (here: tighter).
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/users/"+me.UID, `{"maxUploadBytes":50}`); rec.Code != http.StatusOK {
		t.Fatalf("set user override = %d", rec.Code)
	}
	if rec := uploadBytes(t, c, "demo", 200); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload over user override = %d, want 413", rec.Code)
	}

	// Clearing the user override falls back to the channel exemption.
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/users/"+me.UID, `{"maxUploadBytes":null}`); rec.Code != http.StatusOK {
		t.Fatalf("clear user override = %d", rec.Code)
	}
	if rec := uploadBytes(t, c, "demo", 200); rec.Code != http.StatusCreated {
		t.Fatalf("upload after clearing user override = %d, want 201", rec.Code)
	}

	// Invalid values are rejected.
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/users/"+me.UID, `{"maxUploadBytes":-1}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("negative override = %d, want 400", rec.Code)
	}
}

func TestAdminRuntimeConfig(t *testing.T) {
	s := newAdminServer(t, Options{MaxUploadBytes: 1 << 20, MaxChannelsPerUser: 1})
	c := newClient(t, s)

	// Effective config starts at the configured base.
	rec := adminDo(t, s, testAdminToken, "GET", "/api/admin/config", "")
	var cfg struct {
		Base      runtimeConfigValues `json:"base"`
		Effective runtimeConfigValues `json:"effective"`
	}
	json.Unmarshal(rec.Body.Bytes(), &cfg)
	if cfg.Base.MaxUploadBytes != 1<<20 || cfg.Effective.MaxUploadBytes != 1<<20 {
		t.Fatalf("initial config = %+v", cfg)
	}

	// Lowering the upload limit applies immediately.
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/config", `{"maxUploadBytes":50}`); rec.Code != http.StatusOK {
		t.Fatalf("patch config = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := uploadBytes(t, c, "demo", 100); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload over runtime limit = %d, want 413", rec.Code)
	}

	// Raising the per-user channel quota applies immediately.
	if rec := c.do("POST", "/api/channels", "application/json", `{"name":"one"}`); rec.Code != http.StatusCreated {
		t.Fatalf("first channel = %d", rec.Code)
	}
	if rec := c.do("POST", "/api/channels", "application/json", `{"name":"two"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("over-quota channel = %d, want 403", rec.Code)
	}
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/config", `{"maxChannelsPerUser":2}`); rec.Code != http.StatusOK {
		t.Fatalf("raise quota = %d", rec.Code)
	}
	if rec := c.do("POST", "/api/channels", "application/json", `{"name":"two"}`); rec.Code != http.StatusCreated {
		t.Fatalf("channel after raised quota = %d, want 201", rec.Code)
	}

	// Clearing an override reverts to the base value.
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/config", `{"maxUploadBytes":null}`); rec.Code != http.StatusOK {
		t.Fatalf("clear override = %d", rec.Code)
	}
	if rec := uploadBytes(t, c, "demo", 100); rec.Code != http.StatusCreated {
		t.Fatalf("upload after clearing override = %d, want 201", rec.Code)
	}

	// Invalid values are rejected without changing anything.
	if rec := adminDo(t, s, testAdminToken, "PATCH", "/api/admin/config", `{"maxMessagesPerChannel":0}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("zero maxMessagesPerChannel = %d, want 400", rec.Code)
	}
}

func TestAdminStatePersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nekodrop.db")
	store, err := storage.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	s1 := newAdminServer(t, Options{Store: store})
	c := newClient(t, s1)
	rec := c.do("GET", "/api/me", "", "")
	var me struct{ UID string }
	json.Unmarshal(rec.Body.Bytes(), &me)

	adminDo(t, s1, testAdminToken, "PATCH", "/api/admin/users/"+me.UID, `{"banned":true}`)
	adminDo(t, s1, testAdminToken, "PATCH", "/api/admin/config", `{"maxUploadBytes":50}`)
	store.Close()

	store2, err := storage.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()
	s2 := newAdminServer(t, Options{Store: store2})

	// The same visitor (same cookie) is still banned after the restart.
	c2 := &cookieClient{t: t, s: s2, cookie: c.cookie}
	if rec := c2.do("POST", "/api/messages/demo", "application/json", `{"text":"hi"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("banned user after restart = %d, want 403", rec.Code)
	}
	// The runtime config override survived too.
	rec = adminDo(t, s2, testAdminToken, "GET", "/api/admin/config", "")
	var cfg struct {
		Effective runtimeConfigValues `json:"effective"`
	}
	json.Unmarshal(rec.Body.Bytes(), &cfg)
	if cfg.Effective.MaxUploadBytes != 50 {
		t.Fatalf("runtime override after restart = %+v, want maxUploadBytes 50", cfg.Effective)
	}
}
