package workspace

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

const touchEvery = time.Minute

var (
	ErrFull     = errors.New("limite de workspaces atingido")
	ErrNotFound = errors.New("workspace não encontrado")
)

type Info struct {
	ID        string
	Name      string
	CreatedAt time.Time
	LastSeen  time.Time
}

type Manager struct {
	dir     string
	reg     *sql.DB
	max     int
	now     func() time.Time
	mu      sync.Mutex
	open    map[string]*store.Store
	touched map[string]time.Time
}

func NewManager(dataDir string, max int) (*Manager, error) {
	dir := filepath.Join(dataDir, "workspaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.Join(dataDir, "registry.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	reg, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	reg.SetMaxOpenConns(1)
	if _, err := reg.Exec(`CREATE TABLE IF NOT EXISTS workspaces (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at TEXT NOT NULL,
		last_seen_at TEXT NOT NULL
	)`); err != nil {
		return nil, err
	}
	return &Manager{dir: dir, reg: reg, max: max, now: time.Now, open: map[string]*store.Store{}, touched: map[string]time.Time{}}, nil
}

func (m *Manager) Files(id string) []string {
	var found []string
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(m.path(id) + suffix); err == nil {
			found = append(found, m.path(id)+suffix)
		}
	}
	return found
}

func (m *Manager) SetClock(now func() time.Time) {
	m.now = now
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, st := range m.open {
		_ = st.Close()
		delete(m.open, id)
	}
	_ = m.reg.Close()
}

func (m *Manager) path(id string) string {
	return filepath.Join(m.dir, id+".db")
}

func (m *Manager) stamp() string {
	return m.now().UTC().Format(store.TimeFormat)
}

func (m *Manager) Count() (int, error) {
	var n int
	err := m.reg.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&n)
	return n, err
}

func (m *Manager) info(id string) (Info, bool, error) {
	var in Info
	var created, seen string
	err := m.reg.QueryRow(`SELECT id, name, created_at, last_seen_at FROM workspaces WHERE id = ?`, id).Scan(&in.ID, &in.Name, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return Info{}, false, nil
	}
	if err != nil {
		return Info{}, false, err
	}
	in.CreatedAt, _ = time.Parse(store.TimeFormat, created)
	in.LastSeen, _ = time.Parse(store.TimeFormat, seen)
	return in, true, nil
}

func (m *Manager) storeFor(id string) (*store.Store, error) {
	if st, ok := m.open[id]; ok {
		return st, nil
	}
	st, err := store.Open(m.path(id))
	if err != nil {
		return nil, err
	}
	st.SetKeyScope(id)
	if empty, err := st.Empty(); err == nil && empty {
		if err := st.Seed(); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	m.open[id] = st
	return st, nil
}

func (m *Manager) Ensure(id, name string) (*store.Store, Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	in, found, err := m.info(id)
	if err != nil {
		return nil, Info{}, err
	}
	if !found {
		count, err := m.Count()
		if err != nil {
			return nil, Info{}, err
		}
		if m.max > 0 && count >= m.max {
			return nil, Info{}, ErrFull
		}
	}
	st, err := m.storeFor(id)
	if err != nil {
		return nil, Info{}, err
	}
	now := m.stamp()
	if _, err := m.reg.Exec(`INSERT INTO workspaces (id, name, created_at, last_seen_at) VALUES (?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, last_seen_at = excluded.last_seen_at`, id, name, now, now); err != nil {
		return nil, Info{}, err
	}
	m.touched[id] = m.now()
	in, _, err = m.info(id)
	return st, in, err
}

func (m *Manager) Get(id string) (*store.Store, Info, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	in, found, err := m.info(id)
	if err != nil || !found {
		return nil, Info{}, false, err
	}
	st, err := m.storeFor(id)
	if err != nil {
		return nil, Info{}, false, err
	}
	return st, in, true, nil
}

func (m *Manager) Touch(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if last, ok := m.touched[id]; ok && m.now().Sub(last) < touchEvery {
		return nil
	}
	m.touched[id] = m.now()
	_, err := m.reg.Exec(`UPDATE workspaces SET last_seen_at = ? WHERE id = ?`, m.stamp(), id)
	return err
}

func (m *Manager) Idle(before time.Time) ([]Info, error) {
	rows, err := m.reg.Query(`SELECT id, name, created_at, last_seen_at FROM workspaces WHERE last_seen_at < ? ORDER BY last_seen_at`, before.UTC().Format(store.TimeFormat))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Info
	for rows.Next() {
		var in Info
		var created, seen string
		if err := rows.Scan(&in.ID, &in.Name, &created, &seen); err != nil {
			return nil, err
		}
		in.CreatedAt, _ = time.Parse(store.TimeFormat, created)
		in.LastSeen, _ = time.Parse(store.TimeFormat, seen)
		out = append(out, in)
	}
	return out, rows.Err()
}

func (m *Manager) removeFiles(id string) error {
	if st, ok := m.open[id]; ok {
		_ = st.Close()
		delete(m.open, id)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(m.path(id) + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removendo o workspace %s: %w", id, err)
		}
	}
	return nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.removeFiles(id); err != nil {
		return err
	}
	delete(m.touched, id)
	_, err := m.reg.Exec(`DELETE FROM workspaces WHERE id = ?`, id)
	return err
}

func (m *Manager) Reset(id string) (*store.Store, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, found, err := m.info(id); err != nil || !found {
		if err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	if err := m.removeFiles(id); err != nil {
		return nil, err
	}
	st, err := m.storeFor(id)
	if err != nil {
		return nil, err
	}
	m.touched[id] = m.now()
	_, err = m.reg.Exec(`UPDATE workspaces SET last_seen_at = ? WHERE id = ?`, m.stamp(), id)
	return st, err
}
