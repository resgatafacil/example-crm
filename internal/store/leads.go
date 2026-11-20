package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

var Stages = []string{"lead", "avaliado", "cliente"}

type Lead struct {
	ID         string
	Nome       string
	Celular    string
	Email      string
	Estagio    string
	OptOut     bool
	UnitID     string
	UnitName   string
	FlowID     string
	FlowName   string
	StepID     string
	StepName   string
	CriadoEm   time.Time
	AlteradoEm time.Time
}

type LeadFilter struct {
	UnitID string
	FlowID string
	StepID string
	Stage  string
	Search string
	Cursor string
	Limit  int
}

type Page struct {
	Leads      []Lead
	HasMore    bool
	NextCursor string
}

var ErrBadCursor = errors.New("cursor inválido")

const leadSelect = `SELECT l.id, l.nome, l.celular, COALESCE(l.email,''), l.estagio, l.opt_out,
	l.unit_id, u.nome, l.flow_id, f.nome, l.step_id, s.nome, l.criado_em, l.alterado_em
	FROM leads l
	JOIN units u ON u.id = l.unit_id
	JOIN flows f ON f.id = l.flow_id
	JOIN steps s ON s.id = l.step_id`

type scanner interface {
	Scan(dest ...any) error
}

func scanLead(row scanner) (Lead, error) {
	var l Lead
	var created, changed string
	err := row.Scan(&l.ID, &l.Nome, &l.Celular, &l.Email, &l.Estagio, &l.OptOut,
		&l.UnitID, &l.UnitName, &l.FlowID, &l.FlowName, &l.StepID, &l.StepName, &created, &changed)
	if err != nil {
		return l, err
	}
	l.CriadoEm, _ = time.Parse(TimeFormat, created)
	l.AlteradoEm, _ = time.Parse(TimeFormat, changed)
	return l, nil
}

func EncodeCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(TimeFormat) + "|" + id))
}

func decodeCursor(cursor string) (string, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", "", ErrBadCursor
	}
	created, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return "", "", ErrBadCursor
	}
	if _, err := time.Parse(TimeFormat, created); err != nil {
		return "", "", ErrBadCursor
	}
	return created, id, nil
}

func (s *Store) LeadsPage(f LeadFilter) (Page, error) {
	var where []string
	var args []any
	add := func(clause string, value any) {
		where = append(where, clause)
		args = append(args, value)
	}
	if f.UnitID != "" {
		add("l.unit_id = ?", f.UnitID)
	}
	if f.FlowID != "" {
		add("l.flow_id = ?", f.FlowID)
	}
	if f.StepID != "" {
		add("l.step_id = ?", f.StepID)
	}
	if f.Stage != "" {
		add("l.estagio = ?", f.Stage)
	}
	if f.Search != "" {
		clause := "LOWER(l.nome) LIKE ?"
		args = append(args, "%"+strings.ToLower(f.Search)+"%")
		if digits := Digits(f.Search); digits != "" {
			clause += " OR l.celular_digits LIKE ?"
			args = append(args, "%"+digits+"%")
		}
		where = append(where, "("+clause+")")
	}
	if f.Cursor != "" {
		created, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return Page{}, err
		}
		where = append(where, "(l.criado_em > ? OR (l.criado_em = ? AND l.id > ?))")
		args = append(args, created, created, id)
	}

	query := leadSelect
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit < 1 {
		limit = 50
	}
	query += " ORDER BY l.criado_em, l.id LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()

	var page Page
	for rows.Next() {
		lead, err := scanLead(rows)
		if err != nil {
			return Page{}, err
		}
		page.Leads = append(page.Leads, lead)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Leads) > limit {
		page.Leads = page.Leads[:limit]
		last := page.Leads[limit-1]
		page.HasMore = true
		page.NextCursor = EncodeCursor(last.CriadoEm, last.ID)
	}
	return page, nil
}

func (s *Store) Lead(id string) (Lead, bool, error) {
	lead, err := scanLead(s.db.QueryRow(leadSelect+" WHERE l.id = ?", id))
	if err == sql.ErrNoRows {
		return Lead{}, false, nil
	}
	return lead, err == nil, err
}

func (s *Store) LeadByPhone(phone string) (Lead, bool, error) {
	digits := Digits(phone)
	if digits == "" {
		return Lead{}, false, nil
	}
	lead, err := scanLead(s.db.QueryRow(leadSelect+" WHERE l.celular_digits = ? ORDER BY l.criado_em LIMIT 1", digits))
	if err == sql.ErrNoRows {
		return Lead{}, false, nil
	}
	return lead, err == nil, err
}

type LeadInput struct {
	Nome    string
	Celular string
	Email   string
	Estagio string
	UnitID  string
	StepID  string
}

func (s *Store) CreateLead(in LeadInput) (string, error) {
	step, ok, err := s.Step(in.StepID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("etapa desconhecida")
	}
	id := newID("lead_", 8)
	now := s.stamp()
	_, err = s.db.Exec(`INSERT INTO leads (id, nome, celular, celular_digits, email, estagio, opt_out, unit_id, flow_id, step_id, criado_em, alterado_em)
		VALUES (?,?,?,?,?,?,0,?,?,?,?,?)`,
		id, in.Nome, E164(in.Celular), Digits(E164(in.Celular)), in.Email, in.Estagio, in.UnitID, step.FlowID, step.ID, now, now)
	return id, err
}

func (s *Store) UpdateLead(id string, in LeadInput) error {
	_, err := s.db.Exec(`UPDATE leads SET nome = ?, celular = ?, celular_digits = ?, email = ?, estagio = ?, unit_id = ?, alterado_em = ? WHERE id = ?`,
		in.Nome, E164(in.Celular), Digits(E164(in.Celular)), in.Email, in.Estagio, in.UnitID, s.stamp(), id)
	return err
}

func (s *Store) MoveLead(id, stepID string) error {
	step, ok, err := s.Step(stepID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("etapa desconhecida")
	}
	_, err = s.db.Exec(`UPDATE leads SET step_id = ?, flow_id = ?, alterado_em = ? WHERE id = ?`, step.ID, step.FlowID, s.stamp(), id)
	return err
}

func (s *Store) SetOptOut(id string, optOut bool) error {
	_, err := s.db.Exec(`UPDATE leads SET opt_out = ?, alterado_em = ? WHERE id = ?`, optOut, s.stamp(), id)
	return err
}

type Stats struct {
	Leads    int
	Sendable int
	OptOuts  int
	Messages int
	Events   int
}

func (s *Store) Stats() (Stats, error) {
	var st Stats
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM leads),
		(SELECT COUNT(*) FROM leads WHERE opt_out = 0),
		(SELECT COUNT(*) FROM leads WHERE opt_out = 1),
		(SELECT COUNT(*) FROM timeline WHERE kind = 'message_out'),
		(SELECT COUNT(*) FROM events WHERE signature_ok = 1)`).Scan(&st.Leads, &st.Sendable, &st.OptOuts, &st.Messages, &st.Events)
	return st, err
}
