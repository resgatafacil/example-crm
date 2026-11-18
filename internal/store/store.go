package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const TimeFormat = "2006-01-02T15:04:05.000000Z"

type Store struct {
	db       *sql.DB
	now      func() time.Time
	keyScope string
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	} else {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) SetKeyScope(scope string) {
	s.keyScope = scope
}

func (s *Store) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Store) stamp() string {
	return s.now().UTC().Format(TimeFormat)
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

func newID(prefix string, n int) string {
	return prefix + randomHex(n)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func wrap(err error, what string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", what, err)
}

const schema = `
CREATE TABLE IF NOT EXISTS units (
	id TEXT PRIMARY KEY,
	nome TEXT NOT NULL,
	cidade TEXT,
	estado TEXT,
	ativa INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS flows (
	id TEXT PRIMARY KEY,
	nome TEXT NOT NULL,
	ativo INTEGER NOT NULL DEFAULT 1,
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS steps (
	id TEXT PRIMARY KEY,
	flow_id TEXT NOT NULL REFERENCES flows(id),
	nome TEXT NOT NULL,
	ordem INTEGER NOT NULL,
	inicial INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS leads (
	id TEXT PRIMARY KEY,
	nome TEXT NOT NULL,
	celular TEXT NOT NULL,
	celular_digits TEXT NOT NULL,
	email TEXT,
	estagio TEXT NOT NULL,
	opt_out INTEGER NOT NULL DEFAULT 0,
	unit_id TEXT NOT NULL REFERENCES units(id),
	flow_id TEXT NOT NULL REFERENCES flows(id),
	step_id TEXT NOT NULL REFERENCES steps(id),
	criado_em TEXT NOT NULL,
	alterado_em TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS leads_page ON leads (criado_em, id);
CREATE INDEX IF NOT EXISTS leads_digits ON leads (celular_digits);
CREATE TABLE IF NOT EXISTS timeline (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	lead_id TEXT NOT NULL REFERENCES leads(id) ON DELETE CASCADE,
	kind TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	error_code TEXT NOT NULL DEFAULT '',
	external_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS timeline_lead ON timeline (lead_id, id);
CREATE INDEX IF NOT EXISTS timeline_external ON timeline (external_id);
CREATE TABLE IF NOT EXISTS api_keys (
	id TEXT PRIMARY KEY,
	unit_id TEXT NOT NULL REFERENCES units(id),
	name TEXT NOT NULL,
	prefix TEXT NOT NULL,
	hash TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	last_used_at TEXT
);
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT UNIQUE,
	delivery_id TEXT NOT NULL DEFAULT '',
	attempt INTEGER NOT NULL DEFAULT 0,
	type TEXT NOT NULL DEFAULT '',
	signature_ok INTEGER NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	processed INTEGER NOT NULL DEFAULT 0,
	payload TEXT NOT NULL,
	received_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_type ON events (type, id);
CREATE TABLE IF NOT EXISTS connection (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	connection_id TEXT NOT NULL,
	token_encrypted TEXT NOT NULL,
	scope TEXT NOT NULL DEFAULT '',
	external_ref TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	connected_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS oauth_states (
	state TEXT PRIMARY KEY,
	verifier TEXT NOT NULL,
	created_at TEXT NOT NULL
);
`
