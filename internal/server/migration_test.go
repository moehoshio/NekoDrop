package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/moehoshio/NekoDrop/internal/storage"
)

type migrationStatus struct {
	Enabled bool   `json:"enabled"`
	Code    string `json:"code"`
}

func migrationOf(t *testing.T, c *cookieClient) migrationStatus {
	t.Helper()
	rec := c.do("GET", "/api/me/migration", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me/migration = %d", rec.Code)
	}
	var st migrationStatus
	json.Unmarshal(rec.Body.Bytes(), &st)
	return st
}

func TestMigrationDefaultOff(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)

	rec := c.do("GET", "/api/me", "", "")
	var me struct {
		UID       string `json:"uid"`
		Migration bool   `json:"migration"`
	}
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Migration {
		t.Fatal("migration should be off by default")
	}
	if st := migrationOf(t, c); st.Enabled || st.Code != "" {
		t.Fatalf("default migration status = %+v, want disabled with no code", st)
	}
}

func TestMigrationFlowAcrossBrowsers(t *testing.T) {
	s := newTestServer(t)

	// First browser: name the account and enable migration.
	alice := newClient(t, s)
	rec := alice.do("POST", "/api/me", "application/json", `{"name":"Alice"}`)
	var me struct{ UID string }
	json.Unmarshal(rec.Body.Bytes(), &me)

	rec = alice.do("POST", "/api/me/migration", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("enable migration = %d", rec.Code)
	}
	var st migrationStatus
	json.Unmarshal(rec.Body.Bytes(), &st)
	if !st.Enabled || st.Code == "" {
		t.Fatalf("enable migration returned %+v", st)
	}
	// The status endpoint reports the same persistent code.
	if again := migrationOf(t, alice); again.Code != st.Code {
		t.Fatalf("status code %q != minted code %q", again.Code, st.Code)
	}

	// Second browser (no cookie yet) adopts the account with the code.
	other := newClient(t, s)
	rec = other.do("POST", "/api/migrate", "application/json", `{"code":"`+st.Code+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate = %d: %s", rec.Code, rec.Body.String())
	}
	var migrated struct {
		UID       string `json:"uid"`
		Name      string `json:"name"`
		Migration bool   `json:"migration"`
	}
	json.Unmarshal(rec.Body.Bytes(), &migrated)
	if migrated.UID != me.UID || migrated.Name != "Alice" || !migrated.Migration {
		t.Fatalf("migrated identity = %+v, want Alice/%s with migration on", migrated, me.UID)
	}

	// The second browser now IS the same account: /api/me agrees, and the code
	// stays valid (persistent) so more browsers could follow.
	rec = other.do("GET", "/api/me", "", "")
	var me2 struct{ UID string }
	json.Unmarshal(rec.Body.Bytes(), &me2)
	if me2.UID != me.UID {
		t.Fatalf("adopted identity UID = %s, want %s", me2.UID, me.UID)
	}
	if st2 := migrationOf(t, other); st2.Code != st.Code {
		t.Fatalf("code changed after migration: %q != %q", st2.Code, st.Code)
	}
}

func TestMigrateUnknownCodeRejected(t *testing.T) {
	s := newTestServer(t)
	c := newClient(t, s)
	if rec := c.do("POST", "/api/migrate", "application/json", `{"code":"nope"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown code = %d, want 404", rec.Code)
	}
	if rec := c.do("POST", "/api/migrate", "application/json", `{"code":""}`); rec.Code != http.StatusNotFound {
		t.Fatalf("empty code = %d, want 404", rec.Code)
	}
}

func TestMigrationDisableAndRegenerateInvalidateOldCode(t *testing.T) {
	s := newTestServer(t)
	alice := newClient(t, s)

	rec := alice.do("POST", "/api/me/migration", "", "")
	var first migrationStatus
	json.Unmarshal(rec.Body.Bytes(), &first)

	// Regenerating replaces the code; the old one stops working.
	rec = alice.do("POST", "/api/me/migration", "", "")
	var second migrationStatus
	json.Unmarshal(rec.Body.Bytes(), &second)
	if second.Code == "" || second.Code == first.Code {
		t.Fatalf("regenerate returned %+v (old code %q)", second, first.Code)
	}
	other := newClient(t, s)
	if rec := other.do("POST", "/api/migrate", "application/json", `{"code":"`+first.Code+`"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("stale code = %d, want 404", rec.Code)
	}

	// Disabling invalidates the current code too.
	if rec := alice.do("DELETE", "/api/me/migration", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("disable migration = %d", rec.Code)
	}
	if st := migrationOf(t, alice); st.Enabled || st.Code != "" {
		t.Fatalf("status after disable = %+v", st)
	}
	if rec := other.do("POST", "/api/migrate", "application/json", `{"code":"`+second.Code+`"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled code = %d, want 404", rec.Code)
	}
}

func TestMigrationPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nekodrop.db")
	store, err := storage.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	s1, err := New(Options{MaxUploadBytes: 1 << 20, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	alice := newClient(t, s1)
	alice.do("POST", "/api/me", "application/json", `{"name":"Alice"}`)
	rec := alice.do("POST", "/api/me/migration", "", "")
	var st migrationStatus
	json.Unmarshal(rec.Body.Bytes(), &st)
	rec = alice.do("GET", "/api/me", "", "")
	var me struct{ UID string }
	json.Unmarshal(rec.Body.Bytes(), &me)
	store.Close()

	store2, err := storage.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer store2.Close()
	s2, err := New(Options{MaxUploadBytes: 1 << 20, Store: store2})
	if err != nil {
		t.Fatal(err)
	}
	fresh := newClient(t, s2)
	rec = fresh.do("POST", "/api/migrate", "application/json", `{"code":"`+st.Code+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate after restart = %d: %s", rec.Code, rec.Body.String())
	}
	var migrated struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
	}
	json.Unmarshal(rec.Body.Bytes(), &migrated)
	if migrated.UID != me.UID || migrated.Name != "Alice" {
		t.Fatalf("migrated identity after restart = %+v, want Alice/%s", migrated, me.UID)
	}
}
