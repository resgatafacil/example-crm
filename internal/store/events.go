package store

import (
	"database/sql"
	"time"
)

type Event struct {
	ID          int64
	EventID     string
	DeliveryID  string
	Attempt     int
	Type        string
	SignatureOK bool
	Reason      string
	Processed   bool
	Payload     string
	ReceivedAt  time.Time
}

const maxRejectedEvents = 100

func ignoreNoRows(err error) error {
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}

func (s *Store) ReceiveEvent(e Event) (stored Event, isNew bool, err error) {
	if e.EventID != "" {
		existing, found, err := s.eventByEventID(e.EventID)
		if err != nil {
			return Event{}, false, err
		}
		if found {
			return existing, false, nil
		}
	}
	var eventID any
	if e.EventID != "" {
		eventID = e.EventID
	}
	res, err := s.db.Exec(`INSERT INTO events (event_id, delivery_id, attempt, type, signature_ok, reason, processed, payload, received_at) VALUES (?,?,?,?,?,?,0,?,?)`,
		eventID, e.DeliveryID, e.Attempt, e.Type, e.SignatureOK, e.Reason, e.Payload, s.stamp())
	if err != nil {
		return Event{}, false, err
	}
	e.ID, _ = res.LastInsertId()
	if !e.SignatureOK {
		if err := s.pruneRejected(); err != nil {
			return Event{}, false, err
		}
	}
	return e, true, nil
}

func (s *Store) pruneRejected() error {
	_, err := s.db.Exec(`DELETE FROM events WHERE signature_ok = 0 AND id NOT IN (SELECT id FROM events WHERE signature_ok = 0 ORDER BY id DESC LIMIT ?)`, maxRejectedEvents)
	return err
}

func (s *Store) MarkEventProcessed(id int64, reason string) error {
	_, err := s.db.Exec(`UPDATE events SET processed = 1, reason = ? WHERE id = ?`, reason, id)
	return err
}

func (s *Store) eventByEventID(eventID string) (Event, bool, error) {
	row := s.db.QueryRow(eventSelect+` WHERE event_id = ?`, eventID)
	e, err := scanEvent(row)
	if err != nil {
		return Event{}, false, ignoreNoRows(err)
	}
	return e, true, nil
}

const eventSelect = `SELECT id, COALESCE(event_id,''), delivery_id, attempt, type, signature_ok, reason, processed, payload, received_at FROM events`

func scanEvent(row scanner) (Event, error) {
	var e Event
	var received string
	err := row.Scan(&e.ID, &e.EventID, &e.DeliveryID, &e.Attempt, &e.Type, &e.SignatureOK, &e.Reason, &e.Processed, &e.Payload, &received)
	e.ReceivedAt, _ = time.Parse(TimeFormat, received)
	return e, err
}

func (s *Store) Events(eventType string, limit int) ([]Event, error) {
	query := eventSelect
	var args []any
	if eventType == "rejected" {
		query += ` WHERE signature_ok = 0`
	} else if eventType != "" {
		query += ` WHERE type = ?`
		args = append(args, eventType)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) EventTypes() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT type FROM events WHERE signature_ok = 1 AND type != '' ORDER BY type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) LastValidEvent() (Event, bool, error) {
	e, err := scanEvent(s.db.QueryRow(eventSelect + ` WHERE signature_ok = 1 ORDER BY id DESC LIMIT 1`))
	if err != nil {
		return Event{}, false, ignoreNoRows(err)
	}
	return e, true, nil
}

func (s *Store) CountValidEvents() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE signature_ok = 1`).Scan(&n)
	return n, err
}
