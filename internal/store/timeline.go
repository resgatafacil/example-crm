package store

import "time"

const (
	KindNote       = "note"
	KindMessageOut = "message_out"
	KindMessageIn  = "message_in"
	KindOptOut     = "opt_out"
	KindStep       = "step"
)

type Entry struct {
	ID         int64
	LeadID     string
	Kind       string
	Body       string
	Status     string
	ErrorCode  string
	ExternalID string
	CampaignID string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (s *Store) AddEntry(e Entry) (int64, error) {
	now := s.stamp()
	res, err := s.db.Exec(`INSERT INTO timeline (lead_id, kind, body, status, error_code, external_id, campaign_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		e.LeadID, e.Kind, e.Body, e.Status, e.ErrorCode, e.ExternalID, e.CampaignID, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Timeline(leadID string) ([]Entry, error) {
	rows, err := s.db.Query(`SELECT id, lead_id, kind, body, status, error_code, external_id, campaign_id, created_at, updated_at FROM timeline WHERE lead_id = ? ORDER BY id DESC`, leadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var created, updated string
		if err := rows.Scan(&e.ID, &e.LeadID, &e.Kind, &e.Body, &e.Status, &e.ErrorCode, &e.ExternalID, &e.CampaignID, &created, &updated); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(TimeFormat, created)
		e.UpdatedAt, _ = time.Parse(TimeFormat, updated)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) MessageByExternalID(externalID string) (Entry, bool, error) {
	var e Entry
	var created, updated string
	err := s.db.QueryRow(`SELECT id, lead_id, kind, body, status, error_code, external_id, campaign_id, created_at, updated_at FROM timeline WHERE kind = ? AND external_id = ? LIMIT 1`, KindMessageOut, externalID).
		Scan(&e.ID, &e.LeadID, &e.Kind, &e.Body, &e.Status, &e.ErrorCode, &e.ExternalID, &e.CampaignID, &created, &updated)
	if err != nil {
		return Entry{}, false, ignoreNoRows(err)
	}
	e.CreatedAt, _ = time.Parse(TimeFormat, created)
	e.UpdatedAt, _ = time.Parse(TimeFormat, updated)
	return e, true, nil
}

func (s *Store) UpdateMessageStatus(id int64, status, errorCode string) error {
	_, err := s.db.Exec(`UPDATE timeline SET status = ?, error_code = ?, updated_at = ? WHERE id = ?`, status, errorCode, s.stamp(), id)
	return err
}

func (s *Store) HasPendingMessages(leadID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM timeline WHERE lead_id = ? AND kind = ? AND status NOT IN ('read','failed')`, leadID, KindMessageOut).Scan(&n)
	return n > 0, err
}
