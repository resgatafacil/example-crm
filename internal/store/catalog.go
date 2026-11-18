package store

import "database/sql"

type Unit struct {
	ID     string
	Nome   string
	Cidade string
	Estado string
	Ativa  bool
}

type Step struct {
	ID      string
	FlowID  string
	Nome    string
	Ordem   int
	Inicial bool
}

type Flow struct {
	ID    string
	Nome  string
	Ativo bool
	Steps []Step
}

func (s *Store) Units() ([]Unit, error) {
	rows, err := s.db.Query(`SELECT id, nome, COALESCE(cidade,''), COALESCE(estado,''), ativa FROM units ORDER BY nome`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unit
	for rows.Next() {
		var u Unit
		if err := rows.Scan(&u.ID, &u.Nome, &u.Cidade, &u.Estado, &u.Ativa); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) Unit(id string) (Unit, bool, error) {
	var u Unit
	err := s.db.QueryRow(`SELECT id, nome, COALESCE(cidade,''), COALESCE(estado,''), ativa FROM units WHERE id = ?`, id).
		Scan(&u.ID, &u.Nome, &u.Cidade, &u.Estado, &u.Ativa)
	if err == sql.ErrNoRows {
		return Unit{}, false, nil
	}
	return u, err == nil, err
}

func (s *Store) Flows() ([]Flow, error) {
	rows, err := s.db.Query(`SELECT id, nome, ativo FROM flows ORDER BY position, nome`)
	if err != nil {
		return nil, err
	}
	var flows []Flow
	for rows.Next() {
		var f Flow
		if err := rows.Scan(&f.ID, &f.Nome, &f.Ativo); err != nil {
			rows.Close()
			return nil, err
		}
		flows = append(flows, f)
	}
	rows.Close()
	for i := range flows {
		steps, err := s.stepsOf(flows[i].ID)
		if err != nil {
			return nil, err
		}
		flows[i].Steps = steps
	}
	return flows, nil
}

func (s *Store) stepsOf(flowID string) ([]Step, error) {
	rows, err := s.db.Query(`SELECT id, flow_id, nome, ordem, inicial FROM steps WHERE flow_id = ? ORDER BY ordem`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.ID, &st.FlowID, &st.Nome, &st.Ordem, &st.Inicial); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) Step(id string) (Step, bool, error) {
	var st Step
	err := s.db.QueryRow(`SELECT id, flow_id, nome, ordem, inicial FROM steps WHERE id = ?`, id).
		Scan(&st.ID, &st.FlowID, &st.Nome, &st.Ordem, &st.Inicial)
	if err == sql.ErrNoRows {
		return Step{}, false, nil
	}
	return st, err == nil, err
}

func (s *Store) CreateUnit(u Unit) error {
	_, err := s.db.Exec(`INSERT INTO units (id, nome, cidade, estado, ativa) VALUES (?,?,?,?,?)`, u.ID, u.Nome, u.Cidade, u.Estado, u.Ativa)
	return err
}

func (s *Store) CreateFlow(f Flow, position int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO flows (id, nome, ativo, position) VALUES (?,?,?,?)`, f.ID, f.Nome, f.Ativo, position); err != nil {
		return err
	}
	for _, st := range f.Steps {
		if _, err := tx.Exec(`INSERT INTO steps (id, flow_id, nome, ordem, inicial) VALUES (?,?,?,?,?)`, st.ID, f.ID, st.Nome, st.Ordem, st.Inicial); err != nil {
			return err
		}
	}
	return tx.Commit()
}
