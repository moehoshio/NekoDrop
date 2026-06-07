package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql" // MySQL driver (pure Go).
	_ "modernc.org/sqlite"             // SQLite driver (pure Go, no cgo).
)

// sqlStore is a database-backed Store shared by the sqlite and mysql backends.
// The two dialects differ only in the driver name, blob/text column types and
// the SQLite-specific busy handling, captured by the dialect struct.
type sqlStore struct {
	backend string
	db      *sql.DB

	mu sync.Mutex // serialises writes (SQLite is single-writer).
}

type dialect struct {
	driver   string
	dsn      string
	blobType string
	textType string
	pragmas  []string
}

func openSQL(backend, dsn string) (Store, error) {
	var d dialect
	switch backend {
	case "sqlite":
		if dsn == "" {
			dsn = "nekodrop.db"
		}
		d = dialect{
			driver:   "sqlite",
			dsn:      dsn,
			blobType: "BLOB",
			textType: "TEXT",
			// WAL improves concurrent reads; busy_timeout avoids "database is
			// locked" under brief write contention.
			pragmas: []string{
				"PRAGMA journal_mode=WAL",
				"PRAGMA busy_timeout=5000",
				"PRAGMA foreign_keys=ON",
			},
		}
	case "mysql":
		if dsn == "" {
			return nil, fmt.Errorf("storage: mysql backend requires a DSN (e.g. user:pass@tcp(host:3306)/nekodrop)")
		}
		// parseTime keeps time.Time round-tripping through DATETIME columns.
		if !strings.Contains(dsn, "parseTime=") {
			if strings.Contains(dsn, "?") {
				dsn += "&parseTime=true"
			} else {
				dsn += "?parseTime=true"
			}
		}
		d = dialect{driver: "mysql", dsn: dsn, blobType: "LONGBLOB", textType: "MEDIUMTEXT"}
	default:
		return nil, fmt.Errorf("storage: unsupported sql backend %q", backend)
	}

	db, err := sql.Open(d.driver, d.dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", backend, err)
	}
	if backend == "sqlite" {
		// A single connection sidesteps SQLite's writer contention entirely.
		db.SetMaxOpenConns(1)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("storage: connect %s: %w", backend, err)
	}
	for _, p := range d.pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("storage: %s: %w", p, err)
		}
	}
	s := &sqlStore{backend: backend, db: db}
	if err := s.migrate(d); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *sqlStore) migrate(d dialect) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			token ` + d.textType + ` NOT NULL,
			uid VARCHAR(64) NOT NULL,
			name ` + d.textType + ` NOT NULL,
			named BOOLEAN NOT NULL,
			PRIMARY KEY (uid)
		)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id VARCHAR(64) NOT NULL,
			ckey VARCHAR(160) NOT NULL,
			owner_uid VARCHAR(64) NOT NULL,
			name ` + d.textType + ` NOT NULL,
			description ` + d.textType + ` NOT NULL,
			visibility VARCHAR(16) NOT NULL,
			list_public BOOLEAN NOT NULL,
			allow_join BOOLEAN NOT NULL,
			require_approval BOOLEAN NOT NULL,
			allow_speak BOOLEAN NOT NULL,
			admins ` + d.textType + ` NOT NULL,
			members ` + d.textType + ` NOT NULL,
			banned ` + d.textType + ` NOT NULL,
			muted ` + d.textType + ` NOT NULL,
			names ` + d.textType + ` NOT NULL,
			pending ` + d.textType + ` NOT NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id VARCHAR(64) NOT NULL,
			channel_id VARCHAR(64) NOT NULL,
			kind VARCHAR(16) NOT NULL,
			sender ` + d.textType + ` NOT NULL,
			sender_uid VARCHAR(64) NOT NULL,
			body ` + d.textType + ` NOT NULL,
			mentions ` + d.textType + ` NOT NULL,
			preview BOOLEAN NOT NULL,
			file_id VARCHAR(64) NOT NULL,
			file_name ` + d.textType + ` NOT NULL,
			file_size BIGINT NOT NULL,
			file_type ` + d.textType + ` NOT NULL,
			created DATETIME NOT NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE TABLE IF NOT EXISTS files (
			id VARCHAR(64) NOT NULL,
			channel_id VARCHAR(64) NOT NULL,
			name ` + d.textType + ` NOT NULL,
			content_type ` + d.textType + ` NOT NULL,
			data ` + d.blobType + ` NOT NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE TABLE IF NOT EXISTS announcements (
			id VARCHAR(64) NOT NULL,
			channel_id VARCHAR(64) NOT NULL,
			author_uid VARCHAR(64) NOT NULL,
			author_name ` + d.textType + ` NOT NULL,
			body ` + d.textType + ` NOT NULL,
			created DATETIME NOT NULL,
			PRIMARY KEY (id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_channel ON messages (channel_id)`,
		`CREATE INDEX IF NOT EXISTS idx_announcements_channel ON announcements (channel_id)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}
	return nil
}

func (s *sqlStore) Backend() string { return s.backend }

func (s *sqlStore) Close() error { return s.db.Close() }

// --- Users ---

func (s *sqlStore) LoadUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT token, uid, name, named FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Token, &u.UID, &u.Name, &u.Named); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *sqlStore) SaveUser(u User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(
		`REPLACE INTO users (token, uid, name, named) VALUES (?, ?, ?, ?)`,
		u.Token, u.UID, u.Name, u.Named)
	return err
}

// --- Channels ---

func (s *sqlStore) LoadChannels() ([]Channel, error) {
	rows, err := s.db.Query(`SELECT id, ckey, owner_uid, name, description, visibility,
		list_public, allow_join, require_approval, allow_speak,
		admins, members, banned, muted, names, pending FROM channels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		var admins, members, banned, muted, names, pending string
		if err := rows.Scan(&c.ID, &c.Key, &c.OwnerUID, &c.Name, &c.Description, &c.Visibility,
			&c.ListPublic, &c.AllowJoin, &c.RequireApproval, &c.AllowSpeak,
			&admins, &members, &banned, &muted, &names, &pending); err != nil {
			return nil, err
		}
		c.Admins = decodeSlice(admins)
		c.Members = decodeSlice(members)
		c.Banned = decodeSlice(banned)
		c.Muted = decodeSlice(muted)
		c.Names = decodeMap(names)
		c.Pending = decodeMap(pending)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *sqlStore) SaveChannel(c Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`REPLACE INTO channels
		(id, ckey, owner_uid, name, description, visibility,
		 list_public, allow_join, require_approval, allow_speak,
		 admins, members, banned, muted, names, pending)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Key, c.OwnerUID, c.Name, c.Description, c.Visibility,
		c.ListPublic, c.AllowJoin, c.RequireApproval, c.AllowSpeak,
		encodeSlice(c.Admins), encodeSlice(c.Members), encodeSlice(c.Banned),
		encodeSlice(c.Muted), encodeMap(c.Names), encodeMap(c.Pending))
	return err
}

func (s *sqlStore) DeleteChannel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	return err
}

// --- Messages ---

func (s *sqlStore) LoadMessages(channelID string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id, channel_id, kind, sender, sender_uid, body, mentions,
		preview, file_id, file_name, file_size, file_type, created
		FROM messages WHERE channel_id = ? ORDER BY created ASC, id ASC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var mentions string
		var created time.Time
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.Kind, &m.Sender, &m.SenderUID, &m.Text, &mentions,
			&m.Preview, &m.FileID, &m.FileName, &m.FileSize, &m.FileType, &created); err != nil {
			return nil, err
		}
		m.Mentions = decodeSlice(mentions)
		m.Time = created.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *sqlStore) AppendMessage(m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`REPLACE INTO messages
		(id, channel_id, kind, sender, sender_uid, body, mentions,
		 preview, file_id, file_name, file_size, file_type, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ChannelID, m.Kind, m.Sender, m.SenderUID, m.Text, encodeSlice(m.Mentions),
		m.Preview, m.FileID, m.FileName, m.FileSize, m.FileType, m.Time.UTC())
	return err
}

// --- Files ---

func (s *sqlStore) LoadFile(id string) (File, bool, error) {
	var f File
	err := s.db.QueryRow(`SELECT id, channel_id, name, content_type, data FROM files WHERE id = ?`, id).
		Scan(&f.ID, &f.ChannelID, &f.Name, &f.ContentType, &f.Data)
	if err == sql.ErrNoRows {
		return File{}, false, nil
	}
	if err != nil {
		return File{}, false, err
	}
	return f, true, nil
}

func (s *sqlStore) SaveFile(f File) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`REPLACE INTO files (id, channel_id, name, content_type, data) VALUES (?, ?, ?, ?, ?)`,
		f.ID, f.ChannelID, f.Name, f.ContentType, f.Data)
	return err
}

// --- Announcements ---

func (s *sqlStore) LoadAnnouncements(channelID string) ([]Announcement, error) {
	rows, err := s.db.Query(`SELECT id, channel_id, author_uid, author_name, body, created
		FROM announcements WHERE channel_id = ? ORDER BY created ASC, id ASC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Announcement
	for rows.Next() {
		var a Announcement
		var created time.Time
		if err := rows.Scan(&a.ID, &a.ChannelID, &a.AuthorUID, &a.AuthorName, &a.Text, &created); err != nil {
			return nil, err
		}
		a.Time = created.UTC()
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *sqlStore) AppendAnnouncement(a Announcement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`REPLACE INTO announcements (id, channel_id, author_uid, author_name, body, created)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.ChannelID, a.AuthorUID, a.AuthorName, a.Text, a.Time.UTC())
	return err
}

// --- JSON helpers for slice/map columns ---

func encodeSlice(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeSlice(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var v []string
	_ = json.Unmarshal([]byte(s), &v)
	return v
}

func encodeMap(v map[string]string) string {
	if len(v) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func decodeMap(s string) map[string]string {
	if s == "" || s == "{}" {
		return map[string]string{}
	}
	v := map[string]string{}
	_ = json.Unmarshal([]byte(s), &v)
	return v
}
