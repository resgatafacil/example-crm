package store

import (
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/crypto"
)

const KeyPrefix = "crm_"

type APIKey struct {
	ID         string
	UnitID     string
	UnitName   string
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

func (s *Store) CreateAPIKey(unitID, name string) (string, error) {
	scoped := KeyPrefix
	if s.keyScope != "" {
		scoped += s.keyScope + "_"
	}
	secret, err := crypto.RandomToken(scoped, 24)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO api_keys (id, unit_id, name, prefix, hash, created_at) VALUES (?,?,?,?,?,?)`,
		newID("key_", 6), unitID, name, secret[:len(KeyPrefix)+6], crypto.Hash(secret), s.stamp())
	return secret, err
}

func (s *Store) AuthenticateKey(secret string) (APIKey, bool, error) {
	var k APIKey
	var created string
	err := s.db.QueryRow(`SELECT id, unit_id, name, prefix, created_at FROM api_keys WHERE hash = ?`, crypto.Hash(secret)).
		Scan(&k.ID, &k.UnitID, &k.Name, &k.Prefix, &created)
	if err != nil {
		return APIKey{}, false, ignoreNoRows(err)
	}
	k.CreatedAt, _ = time.Parse(TimeFormat, created)
	_, err = s.db.Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, s.stamp(), k.ID)
	return k, true, err
}

func (s *Store) APIKeys() ([]APIKey, error) {
	rows, err := s.db.Query(`SELECT k.id, k.unit_id, u.nome, k.name, k.prefix, k.created_at, k.last_used_at FROM api_keys k JOIN units u ON u.id = k.unit_id ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		var created string
		var used *string
		if err := rows.Scan(&k.ID, &k.UnitID, &k.UnitName, &k.Name, &k.Prefix, &created, &used); err != nil {
			return nil, err
		}
		k.CreatedAt, _ = time.Parse(TimeFormat, created)
		if used != nil {
			t, _ := time.Parse(TimeFormat, *used)
			k.LastUsedAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAPIKey(id string) error {
	_, err := s.db.Exec(`DELETE FROM api_keys WHERE id = ?`, id)
	return err
}
