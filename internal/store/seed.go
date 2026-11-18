package store

import "time"

type seedLead struct {
	nome, celular, email, estagio, unit, step string
	optOut                                    bool
	note                                      string
}

func (s *Store) Empty() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM units`).Scan(&n)
	return n == 0, err
}

func (s *Store) Seed() error {
	units := []Unit{
		{ID: "un_centro", Nome: "Clínica Sorriso Centro", Cidade: "Campinas", Estado: "SP", Ativa: true},
		{ID: "un_cambui", Nome: "Clínica Sorriso Cambuí", Cidade: "Campinas", Estado: "SP", Ativa: true},
		{ID: "un_norte", Nome: "Clínica Sorriso Norte", Cidade: "Valinhos", Estado: "SP", Ativa: false},
	}
	for _, u := range units {
		if err := s.CreateUnit(u); err != nil {
			return err
		}
	}

	flows := []Flow{
		{ID: "fl_agendamento", Nome: "Agendamento", Ativo: true, Steps: []Step{
			{ID: "st_novo", Nome: "Novo lead", Ordem: 1, Inicial: true},
			{ID: "st_contato", Nome: "Contato feito", Ordem: 2},
			{ID: "st_agendado", Nome: "Avaliação agendada", Ordem: 3},
			{ID: "st_compareceu", Nome: "Compareceu", Ordem: 4},
		}},
		{ID: "fl_reativacao", Nome: "Reativação", Ativo: true, Steps: []Step{
			{ID: "st_inativo", Nome: "Inativo", Ordem: 1, Inicial: true},
			{ID: "st_reengajado", Nome: "Reengajado", Ordem: 2},
		}},
	}
	for i, f := range flows {
		if err := s.CreateFlow(f, i); err != nil {
			return err
		}
	}

	leads := []seedLead{
		{"Mariana Souza", "+5519991230001", "mariana.souza@example.com", "lead", "un_centro", "st_novo", false, ""},
		{"Rafael Almeida", "+5519991230002", "rafael.almeida@example.com", "lead", "un_centro", "st_novo", false, ""},
		{"Camila Ferreira", "+5519991230003", "camila.ferreira@example.com", "lead", "un_cambui", "st_contato", false, ""},
		{"Bruno Carvalho", "+5519991230004", "", "avaliado", "un_centro", "st_contato", false, ""},
		{"Patrícia Lima", "+5519991230005", "patricia.lima@example.com", "avaliado", "un_cambui", "st_agendado", false, ""},
		{"Eduardo Martins", "+5519991230006", "", "avaliado", "un_centro", "st_agendado", false, ""},
		{"Juliana Costa", "+5519991230007", "juliana.costa@example.com", "cliente", "un_cambui", "st_compareceu", false, ""},
		{"Thiago Ribeiro", "+5519991230008", "", "cliente", "un_centro", "st_compareceu", false, ""},
		{"Fernanda Rocha", "+5519991230009", "fernanda.rocha@example.com", "lead", "un_norte", "st_novo", false, ""},
		{"Lucas Pereira", "+5519991230010", "lucas.pereira@example.com", "lead", "un_centro", "st_inativo", true, "Pediu para não receber mais mensagens."},
		{"Aline Barbosa", "+5519991230011", "", "lead", "un_cambui", "st_inativo", false, ""},
		{"Diego Nogueira", "+5519991230012", "diego.nogueira@example.com", "avaliado", "un_centro", "st_reengajado", false, ""},
		{"Teste do sandbox: falha na entrega", "+5500000000003", "", "lead", "un_centro", "st_novo", false, "Número mágico do sandbox 5500000000003: a mensagem falha com o código 131049."},
		{"Teste do sandbox: responde sozinho", "+5500000000031", "", "lead", "un_centro", "st_novo", false, "Número mágico do sandbox 5500000000031: depois de lida, chega uma resposta automática."},
		{"Teste do sandbox: pede opt-out", "+5500000000030", "", "lead", "un_centro", "st_novo", false, "Número mágico do sandbox 5500000000030: depois de lida, chega contact.opted_out."},
	}

	base := s.now().UTC().Add(-time.Duration(len(leads)) * time.Hour)
	for i, l := range leads {
		step, _, err := s.Step(l.step)
		if err != nil {
			return err
		}
		created := base.Add(time.Duration(i) * time.Hour).Format(TimeFormat)
		id := newID("lead_", 8)
		if _, err := s.db.Exec(`INSERT INTO leads (id, nome, celular, celular_digits, email, estagio, opt_out, unit_id, flow_id, step_id, criado_em, alterado_em)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, l.nome, l.celular, Digits(l.celular), l.email, l.estagio, l.optOut, l.unit, step.FlowID, l.step, created, created); err != nil {
			return err
		}
		if l.note != "" {
			if _, err := s.AddEntry(Entry{LeadID: id, Kind: KindNote, Body: l.note}); err != nil {
				return err
			}
		}
		if l.optOut {
			if _, err := s.AddEntry(Entry{LeadID: id, Kind: KindOptOut, Body: "Pediu para parar de receber mensagens."}); err != nil {
				return err
			}
		}
	}
	return nil
}
