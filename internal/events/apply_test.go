package events

import (
	"encoding/json"
	"strings"
	"testing"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func event(t *testing.T, eventType string, data any) resgata.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return resgata.Event{ID: "evt_" + eventType, Type: eventType, Data: raw}
}

func sendMessage(t *testing.T, s *store.Store, leadPhone, id string) store.Lead {
	t.Helper()
	lead, ok, _ := s.LeadByPhone(leadPhone)
	if !ok {
		t.Fatal("lead not found")
	}
	if _, err := s.AddEntry(store.Entry{LeadID: lead.ID, Kind: store.KindMessageOut, Body: "oi", Status: "queued", ExternalID: id}); err != nil {
		t.Fatal(err)
	}
	return lead
}

func statusOf(t *testing.T, s *store.Store, id string) (string, string) {
	t.Helper()
	entry, ok, _ := s.MessageByExternalID(id)
	if !ok {
		t.Fatal("message entry missing")
	}
	return entry.Status, entry.ErrorCode
}

func status(id, state string) map[string]any {
	return map[string]any{"message": map[string]any{"id": id, "status": state}}
}

func TestStatusEventsAdvanceTheTimeline(t *testing.T) {
	s := newStore(t)
	sendMessage(t, s, "+5519991230001", "msg_1")
	for _, state := range []string{"sent", "delivered", "read"} {
		if _, err := Apply(s, event(t, "message."+state, status("msg_1", state))); err != nil {
			t.Fatal(err)
		}
		if got, _ := statusOf(t, s, "msg_1"); got != state {
			t.Fatalf("after %s status is %s", state, got)
		}
	}
}

func TestOutOfOrderEventsNeverGoBackwards(t *testing.T) {
	s := newStore(t)
	sendMessage(t, s, "+5519991230001", "msg_2")
	for _, state := range []string{"read", "delivered", "sent"} {
		if _, err := Apply(s, event(t, "message."+state, status("msg_2", state))); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := statusOf(t, s, "msg_2"); got != "read" {
		t.Fatalf("late events downgraded the status to %s", got)
	}
}

func TestFailureKeepsErrorCodeAndIsFinal(t *testing.T) {
	s := newStore(t)
	sendMessage(t, s, "+5500000000003", "msg_3")
	failed := map[string]any{"message": map[string]any{"id": "msg_3", "status": "failed"}, "error": map[string]any{"code": 131049}}
	if _, err := Apply(s, event(t, "message.failed", failed)); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(s, event(t, "message.sent", status("msg_3", "sent"))); err != nil {
		t.Fatal(err)
	}
	if got, code := statusOf(t, s, "msg_3"); got != "failed" || code != "131049" {
		t.Fatalf("status %s code %s", got, code)
	}
}

func TestInboundMessageGoesToTheMatchingLead(t *testing.T) {
	s := newStore(t)
	data := map[string]any{"message": map[string]any{"id": "in_1", "from": "5519991230002", "text": "Quero agendar"}}
	result, err := Apply(s, event(t, "message.received", data))
	if err != nil || strings.Contains(result, "new lead") {
		t.Fatalf("%q %v", result, err)
	}
	lead, _, _ := s.LeadByPhone("+5519991230002")
	timeline, _ := s.Timeline(lead.ID)
	if len(timeline) == 0 || timeline[0].Kind != store.KindMessageIn || timeline[0].Body != "Quero agendar" {
		t.Fatalf("timeline: %+v", timeline)
	}
}

func TestInboundFromUnknownContactCreatesLeadInFirstStep(t *testing.T) {
	s := newStore(t)
	data := map[string]any{"message": map[string]any{"id": "in_2", "from": "5511977776666", "from_name": "Nova Pessoa", "text": "Oi"}}
	if _, err := Apply(s, event(t, "message.received", data)); err != nil {
		t.Fatal(err)
	}
	lead, ok, _ := s.LeadByPhone("+5511977776666")
	if !ok || lead.Nome != "Nova Pessoa" || lead.StepID != "st_novo" || lead.Estagio != "lead" {
		t.Fatalf("created lead: %+v %v", lead, ok)
	}
}

func TestOptOutFlagsLeadOnce(t *testing.T) {
	s := newStore(t)
	data := map[string]any{"contact": map[string]any{"phone": "+55 19 99123-0003"}, "matched_phrase": "parar"}
	if _, err := Apply(s, event(t, "contact.opted_out", data)); err != nil {
		t.Fatal(err)
	}
	lead, _, _ := s.LeadByPhone("+5519991230003")
	if !lead.OptOut {
		t.Fatal("lead must be opted out")
	}
	result, _ := Apply(s, event(t, "contact.opted_out", data))
	timeline, _ := s.Timeline(lead.ID)
	optOuts := 0
	for _, e := range timeline {
		if e.Kind == store.KindOptOut {
			optOuts++
		}
	}
	if optOuts != 1 || result != "already opted out" {
		t.Fatalf("opt-out entries %d, result %q", optOuts, result)
	}
}

func TestOptOutOfUnknownContactIsIgnored(t *testing.T) {
	s := newStore(t)
	data := map[string]any{"contact": map[string]any{"phone": "5511900000000"}}
	if _, err := Apply(s, event(t, "contact.opted_out", data)); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionRevokedClearsToken(t *testing.T) {
	s := newStore(t)
	if err := s.SaveConnection(store.Connection{ConnectionID: "conn_1", TokenEncrypted: "sealed", State: store.StateConnected}); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(s, event(t, "connection.revoked", map[string]any{})); err != nil {
		t.Fatal(err)
	}
	conn, _, _ := s.Connection()
	if conn.State != store.StateRevoked || conn.TokenEncrypted != "" {
		t.Fatalf("%+v", conn)
	}
}

func TestPingAndUnknownTypesAreHarmless(t *testing.T) {
	s := newStore(t)
	for _, name := range []string{"ping", "number.connected", "something.new"} {
		if _, err := Apply(s, event(t, name, map[string]any{})); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
