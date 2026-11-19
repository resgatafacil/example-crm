package store

import (
	"database/sql"
	"time"
)

const (
	CampaignScheduled = "scheduled"
	CampaignPending   = "pending"
	CampaignRunning   = "running"
	CampaignPaused    = "paused"
	CampaignFinished  = "finished"
	CampaignFailed    = "failed"
	CampaignCanceled  = "canceled"
)

type Progress struct {
	Total     int
	Sent      int
	Delivered int
	Read      int
	Failed    int
	Skipped   int
}

func (p Progress) Pending() int {
	return max(0, p.Total-p.Sent-p.Failed-p.Skipped)
}

func (p Progress) Percent() int {
	if p.Total <= 0 {
		return 0
	}
	return min(100, (p.Sent+p.Failed+p.Skipped)*100/p.Total)
}

type Campaign struct {
	ID           string
	Name         string
	TemplateID   string
	TemplateName string
	AudienceID   string
	Status       string
	PauseReason  string
	LeadCount    int
	Progress     Progress
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (c Campaign) Active() bool {
	switch c.Status {
	case CampaignScheduled, CampaignPending, CampaignRunning:
		return true
	}
	return false
}

func (c Campaign) Terminal() bool {
	switch c.Status {
	case CampaignFinished, CampaignFailed, CampaignCanceled:
		return true
	}
	return false
}

func (c Campaign) CanPause() bool { return c.Active() }

func (c Campaign) CanResume() bool { return c.Status == CampaignPaused }

const campaignColumns = `id, name, template_id, template_name, audience_id, status, pause_reason, lead_count, total, sent, delivered, read, failed, skipped, created_at, updated_at`

func scanCampaign(row scanner) (Campaign, error) {
	var c Campaign
	var created, updated string
	err := row.Scan(&c.ID, &c.Name, &c.TemplateID, &c.TemplateName, &c.AudienceID, &c.Status, &c.PauseReason, &c.LeadCount,
		&c.Progress.Total, &c.Progress.Sent, &c.Progress.Delivered, &c.Progress.Read, &c.Progress.Failed, &c.Progress.Skipped, &created, &updated)
	if err != nil {
		return c, err
	}
	c.CreatedAt, _ = time.Parse(TimeFormat, created)
	c.UpdatedAt, _ = time.Parse(TimeFormat, updated)
	return c, nil
}

func (s *Store) SaveCampaign(c Campaign) error {
	now := s.stamp()
	_, err := s.db.Exec(`INSERT INTO campaigns (`+campaignColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, template_id = excluded.template_id, template_name = excluded.template_name,
			audience_id = excluded.audience_id, status = excluded.status, pause_reason = excluded.pause_reason, lead_count = excluded.lead_count,
			total = excluded.total, sent = excluded.sent, delivered = excluded.delivered, read = excluded.read, failed = excluded.failed,
			skipped = excluded.skipped, updated_at = excluded.updated_at`,
		c.ID, c.Name, c.TemplateID, c.TemplateName, c.AudienceID, c.Status, c.PauseReason, c.LeadCount,
		c.Progress.Total, c.Progress.Sent, c.Progress.Delivered, c.Progress.Read, c.Progress.Failed, c.Progress.Skipped, now, now)
	return err
}

func (s *Store) Campaign(id string) (Campaign, bool, error) {
	c, err := scanCampaign(s.db.QueryRow(`SELECT `+campaignColumns+` FROM campaigns WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Campaign{}, false, nil
	}
	return c, err == nil, err
}

func (s *Store) Campaigns(limit int) ([]Campaign, error) {
	rows, err := s.db.Query(`SELECT `+campaignColumns+` FROM campaigns ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Campaign
	for rows.Next() {
		c, err := scanCampaign(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) HasCampaign() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM campaigns`).Scan(&n)
	return n > 0, err
}

func (s *Store) SetCampaignRemote(id, status, pauseReason string, progress Progress) error {
	_, err := s.db.Exec(`UPDATE campaigns SET status = ?, pause_reason = ?, total = ?, sent = ?, delivered = ?, read = ?, failed = ?, skipped = ?, updated_at = ? WHERE id = ?`,
		status, pauseReason, progress.Total, progress.Sent, progress.Delivered, progress.Read, progress.Failed, progress.Skipped, s.stamp(), id)
	return err
}

type CampaignMessage struct {
	Entry    Entry
	LeadID   string
	LeadName string
	Phone    string
}

func (s *Store) CampaignMessages(campaignID string) ([]CampaignMessage, error) {
	rows, err := s.db.Query(`SELECT t.id, t.status, t.error_code, t.external_id, t.updated_at, l.id, l.nome, l.celular
		FROM timeline t JOIN leads l ON l.id = t.lead_id
		WHERE t.campaign_id = ? AND t.kind = ? ORDER BY t.id`, campaignID, KindMessageOut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CampaignMessage
	for rows.Next() {
		var m CampaignMessage
		var updated string
		if err := rows.Scan(&m.Entry.ID, &m.Entry.Status, &m.Entry.ErrorCode, &m.Entry.ExternalID, &updated, &m.LeadID, &m.LeadName, &m.Phone); err != nil {
			return nil, err
		}
		m.Entry.UpdatedAt, _ = time.Parse(TimeFormat, updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RefreshCampaignProgress(id string) error {
	var sent, delivered, read, failed int
	err := s.db.QueryRow(`SELECT
		COALESCE(SUM(CASE WHEN status IN ('sent','delivered','read') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status IN ('delivered','read') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'read' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0)
		FROM timeline WHERE campaign_id = ? AND kind = ?`, id, KindMessageOut).Scan(&sent, &delivered, &read, &failed)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE campaigns SET sent = MAX(sent, ?), delivered = MAX(delivered, ?), read = MAX(read, ?), failed = MAX(failed, ?), updated_at = ? WHERE id = ?`,
		sent, delivered, read, failed, s.stamp(), id)
	return err
}
