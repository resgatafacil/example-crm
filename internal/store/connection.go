package store

import "time"

const (
	StateConnected = "connected"
	StateRevoked   = "revoked"
)

type Connection struct {
	ConnectionID   string
	TokenEncrypted string
	Scope          string
	ExternalRef    string
	State          string
	ConnectedAt    time.Time
}

func (s *Store) Connection() (Connection, bool, error) {
	var c Connection
	var at string
	err := s.db.QueryRow(`SELECT connection_id, token_encrypted, scope, external_ref, state, connected_at FROM connection WHERE id = 1`).
		Scan(&c.ConnectionID, &c.TokenEncrypted, &c.Scope, &c.ExternalRef, &c.State, &at)
	if err != nil {
		return Connection{}, false, ignoreNoRows(err)
	}
	c.ConnectedAt, _ = time.Parse(TimeFormat, at)
	return c, true, nil
}

func (s *Store) SaveConnection(c Connection) error {
	_, err := s.db.Exec(`INSERT INTO connection (id, connection_id, token_encrypted, scope, external_ref, state, connected_at) VALUES (1,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET connection_id = excluded.connection_id, token_encrypted = excluded.token_encrypted,
		scope = excluded.scope, external_ref = excluded.external_ref, state = excluded.state, connected_at = excluded.connected_at`,
		c.ConnectionID, c.TokenEncrypted, c.Scope, c.ExternalRef, c.State, s.stamp())
	return err
}

func (s *Store) RevokeConnection() error {
	_, err := s.db.Exec(`UPDATE connection SET state = ?, token_encrypted = '' WHERE id = 1`, StateRevoked)
	return err
}

func (s *Store) SaveOAuthState(state, verifier string) error {
	if _, err := s.db.Exec(`DELETE FROM oauth_states WHERE created_at < ?`, s.now().Add(-time.Hour).UTC().Format(TimeFormat)); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO oauth_states (state, verifier, created_at) VALUES (?,?,?)`, state, verifier, s.stamp())
	return err
}

func (s *Store) ConsumeOAuthState(state string, maxAge time.Duration) (string, bool, error) {
	var verifier, created string
	err := s.db.QueryRow(`SELECT verifier, created_at FROM oauth_states WHERE state = ?`, state).Scan(&verifier, &created)
	if err != nil {
		return "", false, ignoreNoRows(err)
	}
	if _, err := s.db.Exec(`DELETE FROM oauth_states WHERE state = ?`, state); err != nil {
		return "", false, err
	}
	at, _ := time.Parse(TimeFormat, created)
	if s.now().Sub(at) > maxAge {
		return "", false, nil
	}
	return verifier, true, nil
}
