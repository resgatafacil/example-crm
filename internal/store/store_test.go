package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time {
		clock = clock.Add(time.Second)
		return clock
	})
	t.Cleanup(func() { s.Close() })
	if err := s.Seed(); err != nil {
		t.Fatal(err)
	}
	return s
}

func collect(t *testing.T, s *Store, f LeadFilter) []string {
	t.Helper()
	var ids []string
	for {
		page, err := s.LeadsPage(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range page.Leads {
			ids = append(ids, l.ID)
		}
		if !page.HasMore {
			return ids
		}
		f.Cursor = page.NextCursor
	}
}

func TestCursorPaginationVisitsEveryLeadOnce(t *testing.T) {
	s := newStore(t)
	all := collect(t, s, LeadFilter{Limit: 100})
	paged := collect(t, s, LeadFilter{Limit: 4})
	if len(all) != 15 || fmt.Sprint(all) != fmt.Sprint(paged) {
		t.Fatalf("paged traversal differs: %d vs %d", len(all), len(paged))
	}
}

func TestFilters(t *testing.T) {
	s := newStore(t)
	cases := map[string]struct {
		filter LeadFilter
		want   int
	}{
		"unit":   {LeadFilter{UnitID: "un_cambui"}, 4},
		"flow":   {LeadFilter{FlowID: "fl_reativacao"}, 3},
		"step":   {LeadFilter{StepID: "st_agendado"}, 2},
		"stage":  {LeadFilter{Stage: "cliente"}, 2},
		"search": {LeadFilter{Search: "mariana"}, 1},
		"phone":  {LeadFilter{Search: "5500000000031"}, 1},
		"combo":  {LeadFilter{UnitID: "un_centro", Stage: "avaliado"}, 3},
	}
	for name, c := range cases {
		page, err := s.LeadsPage(c.filter)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(page.Leads) != c.want {
			t.Errorf("%s: got %d leads, want %d", name, len(page.Leads), c.want)
		}
	}
}

func TestBadCursor(t *testing.T) {
	s := newStore(t)
	for _, cursor := range []string{"%%%", "bm90LWEtY3Vyc29y", EncodeCursor(time.Now(), "")} {
		if _, err := s.LeadsPage(LeadFilter{Cursor: cursor}); !errors.Is(err, ErrBadCursor) {
			t.Errorf("cursor %q: err = %v", cursor, err)
		}
	}
}

func TestLeadByPhoneIgnoresFormatting(t *testing.T) {
	s := newStore(t)
	for _, phone := range []string{"+55 19 99123-0001", "5519991230001", "19991230001"} {
		lead, ok, err := s.LeadByPhone(E164(phone))
		if err != nil || !ok || lead.Nome != "Mariana Souza" {
			t.Errorf("phone %q: %v %v %q", phone, err, ok, lead.Nome)
		}
	}
}

func TestEventDedupe(t *testing.T) {
	s := newStore(t)
	first, isNew, err := s.ReceiveEvent(Event{EventID: "evt_1", Type: "ping", SignatureOK: true, Payload: "{}"})
	if err != nil || !isNew {
		t.Fatalf("first: %v %v", err, isNew)
	}
	if err := s.MarkEventProcessed(first.ID, "ok"); err != nil {
		t.Fatal(err)
	}
	again, isNew, err := s.ReceiveEvent(Event{EventID: "evt_1", Type: "ping", SignatureOK: true, Payload: "{}"})
	if err != nil || isNew || !again.Processed || again.ID != first.ID {
		t.Fatalf("duplicate must return the stored event: %+v %v %v", again, isNew, err)
	}
}

func TestUnprocessedEventIsRetriedNotLost(t *testing.T) {
	s := newStore(t)
	first, _, _ := s.ReceiveEvent(Event{EventID: "evt_2", Type: "ping", SignatureOK: true, Payload: "{}"})
	again, isNew, _ := s.ReceiveEvent(Event{EventID: "evt_2", Type: "ping", SignatureOK: true, Payload: "{}"})
	if isNew || again.Processed || again.ID != first.ID {
		t.Fatalf("an event that was not processed must come back unprocessed: %+v", again)
	}
}

func TestRejectedEventsAreCapped(t *testing.T) {
	s := newStore(t)
	for i := 0; i < maxRejectedEvents+20; i++ {
		if _, _, err := s.ReceiveEvent(Event{SignatureOK: false, Reason: "bad", Payload: "{}"}); err != nil {
			t.Fatal(err)
		}
	}
	events, _ := s.Events("rejected", 1000)
	if len(events) != maxRejectedEvents {
		t.Fatalf("kept %d rejected events, want %d", len(events), maxRejectedEvents)
	}
}

func TestAPIKeysAreHashedAndShownOnce(t *testing.T) {
	s := newStore(t)
	secret, err := s.CreateAPIKey("un_centro", "Resgata")
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := s.AuthenticateKey(secret)
	if err != nil || !ok || key.UnitID != "un_centro" {
		t.Fatalf("auth: %+v %v %v", key, ok, err)
	}
	if _, ok, _ := s.AuthenticateKey(secret + "x"); ok {
		t.Fatal("wrong key authenticated")
	}
	keys, _ := s.APIKeys()
	if len(keys) != 1 || keys[0].Prefix == secret || keys[0].LastUsedAt == nil {
		t.Fatalf("unexpected listing: %+v", keys)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT hash FROM api_keys`).Scan(&stored); err != nil || stored == secret {
		t.Fatal("the plain key must never be stored")
	}
}

func TestOAuthStateIsSingleUseAndExpires(t *testing.T) {
	s := newStore(t)
	_ = s.SaveOAuthState("state-1", "verifier-1")
	verifier, ok, _ := s.ConsumeOAuthState("state-1", time.Minute)
	if !ok || verifier != "verifier-1" {
		t.Fatalf("first use: %q %v", verifier, ok)
	}
	if _, ok, _ := s.ConsumeOAuthState("state-1", time.Minute); ok {
		t.Fatal("state must be single use")
	}
	_ = s.SaveOAuthState("state-2", "v")
	time.Sleep(10 * time.Millisecond)
	if _, ok, _ := s.ConsumeOAuthState("state-2", time.Nanosecond); ok {
		t.Fatal("expired state must be refused")
	}
}
