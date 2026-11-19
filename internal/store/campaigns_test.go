package store

import (
	"path/filepath"
	"testing"
)

func TestCampaignProgressIsDerivedFromTheTimeline(t *testing.T) {
	s := newStore(t)
	campaign := Campaign{ID: "cmp_1", Name: "Teste", TemplateID: "tpl_1", TemplateName: "promo", AudienceID: "aud_1", Status: CampaignRunning, LeadCount: 3, Progress: Progress{Total: 3}}
	if err := s.SaveCampaign(campaign); err != nil {
		t.Fatal(err)
	}
	statuses := []string{"read", "delivered", "failed"}
	phones := []string{"+5519991230001", "+5519991230002", "+5519991230003"}
	for i, phone := range phones {
		lead, _, _ := s.LeadByPhone(phone)
		if _, err := s.AddEntry(Entry{LeadID: lead.ID, Kind: KindMessageOut, Status: statuses[i], ExternalID: "wamid_" + phone, CampaignID: "cmp_1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RefreshCampaignProgress("cmp_1"); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.Campaign("cmp_1")
	if err != nil || !found {
		t.Fatalf("campaign: %v %v", found, err)
	}
	want := Progress{Total: 3, Sent: 2, Delivered: 2, Read: 1, Failed: 1}
	if got.Progress != want || got.Progress.Pending() != 0 || got.Progress.Percent() != 100 {
		t.Fatalf("progress = %+v", got.Progress)
	}

	messages, err := s.CampaignMessages("cmp_1")
	if err != nil || len(messages) != 3 || messages[2].Entry.Status != "failed" {
		t.Fatalf("messages = %+v %v", messages, err)
	}
	if has, _ := s.HasCampaign(); !has {
		t.Fatal("HasCampaign")
	}
}

func TestProgressNeverGoesBackwards(t *testing.T) {
	s := newStore(t)
	if err := s.SaveCampaign(Campaign{ID: "cmp_2", Name: "x", TemplateID: "t", Status: CampaignRunning, Progress: Progress{Total: 10, Sent: 8, Delivered: 5, Read: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshCampaignProgress("cmp_2"); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Campaign("cmp_2")
	if got.Progress.Sent != 8 || got.Progress.Delivered != 5 || got.Progress.Pending() != 2 || got.Progress.Percent() != 80 {
		t.Fatalf("progress = %+v", got.Progress)
	}
}

func TestCampaignStatusHelpers(t *testing.T) {
	cases := map[string][3]bool{
		CampaignRunning:  {true, true, false},
		CampaignPending:  {true, true, false},
		CampaignPaused:   {false, false, true},
		CampaignFinished: {false, false, false},
	}
	for status, want := range cases {
		c := Campaign{Status: status}
		if c.Active() != want[0] || c.CanPause() != want[1] || c.CanResume() != want[2] {
			t.Errorf("%s: active=%v pause=%v resume=%v", status, c.Active(), c.CanPause(), c.CanResume())
		}
	}
	if !(Campaign{Status: CampaignFailed}).Terminal() || (Campaign{Status: CampaignPaused}).Terminal() {
		t.Fatal("terminal states")
	}
}

func TestMigrationAddsTheCampaignColumnToAnOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP INDEX timeline_campaign; ALTER TABLE timeline DROP COLUMN campaign_id`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopening an old database: %v", err)
	}
	defer s.Close()
	if err := s.Seed(); err != nil {
		t.Fatal(err)
	}
	lead, _, _ := s.LeadByPhone("+5519991230001")
	if _, err := s.AddEntry(Entry{LeadID: lead.ID, Kind: KindMessageOut, ExternalID: "w1", CampaignID: "cmp_9"}); err != nil {
		t.Fatalf("the migrated column must work: %v", err)
	}
	if err := s.migrate(); err != nil {
		t.Fatalf("migrating twice must be harmless: %v", err)
	}
}
