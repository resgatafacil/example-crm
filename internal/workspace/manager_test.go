package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
)

func newManager(t *testing.T, max int) (*Manager, *time.Time) {
	t.Helper()
	m, err := NewManager(t.TempDir(), max)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	now := time.Unix(1_800_000_000, 0)
	m.SetClock(func() time.Time { return now })
	return m, &now
}

func leadCount(t *testing.T, st *store.Store) int {
	t.Helper()
	page, err := st.LeadsPage(store.LeadFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Leads)
}

func TestEnsureCreatesAndSeedsOnce(t *testing.T) {
	m, _ := newManager(t, 5)
	st, info, err := m.Ensure("aaaaaaaaaaaaaaaa", "Maria Souza")
	if err != nil {
		t.Fatal(err)
	}
	seeded := leadCount(t, st)
	if seeded == 0 || info.Name != "Maria Souza" {
		t.Fatalf("seeded=%d info=%+v", seeded, info)
	}
	if _, err := st.CreateLead(store.LeadInput{Nome: "Extra", Celular: "+5511999990000", Estagio: "lead", UnitID: firstUnit(t, st), StepID: firstStep(t, st)}); err != nil {
		t.Fatal(err)
	}
	again, info, err := m.Ensure("aaaaaaaaaaaaaaaa", "Maria S.")
	if err != nil || again != st || info.Name != "Maria S." {
		t.Fatalf("second ensure must reuse the workspace and refresh the name: %v %+v", err, info)
	}
	if leadCount(t, again) != seeded+1 {
		t.Fatal("ensuring again must not seed again")
	}
}

func firstUnit(t *testing.T, st *store.Store) string {
	t.Helper()
	units, err := st.Units()
	if err != nil || len(units) == 0 {
		t.Fatal("no units")
	}
	return units[0].ID
}

func firstStep(t *testing.T, st *store.Store) string {
	t.Helper()
	flows, err := st.Flows()
	if err != nil || len(flows) == 0 || len(flows[0].Steps) == 0 {
		t.Fatal("no flows")
	}
	return flows[0].Steps[0].ID
}

func TestWorkspacesAreIsolated(t *testing.T) {
	m, _ := newManager(t, 5)
	a, _, _ := m.Ensure("aaaaaaaaaaaaaaaa", "A")
	b, _, _ := m.Ensure("bbbbbbbbbbbbbbbb", "B")
	if a == b {
		t.Fatal("each workspace needs its own store")
	}
	if _, err := a.CreateLead(store.LeadInput{Nome: "Só da A", Celular: "+5511988887777", Estagio: "lead", UnitID: firstUnit(t, a), StepID: firstStep(t, a)}); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := b.LeadByPhone("+5511988887777"); found {
		t.Fatal("a lead created in one workspace leaked into another")
	}
	keyA, err := a.CreateAPIKey(firstUnit(t, a), "chave")
	if err != nil {
		t.Fatal(err)
	}
	if _, found, _ := b.AuthenticateKey(keyA); found {
		t.Fatal("an API key leaked across workspaces")
	}
	if keyA[:4] != "crm_" || keyA[4:20] != "aaaaaaaaaaaaaaaa" || keyA[20] != '_' {
		t.Fatalf("keys carry their workspace id: %q", keyA)
	}
}

func TestEnsureStopsAtTheCap(t *testing.T) {
	m, _ := newManager(t, 2)
	for _, id := range []string{"aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb"} {
		if _, _, err := m.Ensure(id, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.Ensure("cccccccccccccccc", "C"); err != ErrFull {
		t.Fatalf("got %v, want ErrFull", err)
	}
	if _, _, err := m.Ensure("aaaaaaaaaaaaaaaa", "A again"); err != nil {
		t.Fatalf("an existing workspace must still get in when the demo is full: %v", err)
	}
}

func TestGetOnlyFindsExistingWorkspaces(t *testing.T) {
	m, _ := newManager(t, 5)
	if _, _, found, err := m.Get("aaaaaaaaaaaaaaaa"); err != nil || found {
		t.Fatalf("unknown workspace: found=%v err=%v", found, err)
	}
	m.Ensure("aaaaaaaaaaaaaaaa", "A")
	if _, info, found, err := m.Get("aaaaaaaaaaaaaaaa"); err != nil || !found || info.Name != "A" {
		t.Fatalf("known workspace: %+v %v %v", info, found, err)
	}
}

func TestIdleAndTouch(t *testing.T) {
	m, now := newManager(t, 5)
	m.Ensure("aaaaaaaaaaaaaaaa", "A")
	m.Ensure("bbbbbbbbbbbbbbbb", "B")
	*now = now.Add(2 * time.Hour)
	if err := m.Touch("bbbbbbbbbbbbbbbb"); err != nil {
		t.Fatal(err)
	}
	idle, err := m.Idle(now.Add(-time.Hour))
	if err != nil || len(idle) != 1 || idle[0].ID != "aaaaaaaaaaaaaaaa" {
		t.Fatalf("idle = %+v %v", idle, err)
	}
}

func TestTouchIsThrottled(t *testing.T) {
	m, now := newManager(t, 5)
	m.Ensure("aaaaaaaaaaaaaaaa", "A")
	first, _ := m.Idle(now.Add(time.Hour))
	*now = now.Add(10 * time.Second)
	m.Touch("aaaaaaaaaaaaaaaa")
	_, info, _, _ := m.Get("aaaaaaaaaaaaaaaa")
	if !info.LastSeen.Equal(first[0].LastSeen) {
		t.Fatal("touching within a minute must not write again")
	}
	*now = now.Add(2 * time.Minute)
	m.Touch("aaaaaaaaaaaaaaaa")
	_, info, _, _ = m.Get("aaaaaaaaaaaaaaaa")
	if !info.LastSeen.After(first[0].LastSeen) {
		t.Fatal("touching after a minute must update last seen")
	}
}

func TestDeleteRemovesFilesAndRegistryRow(t *testing.T) {
	m, _ := newManager(t, 5)
	m.Ensure("aaaaaaaaaaaaaaaa", "A")
	path := m.path("aaaaaaaaaaaaaaaa")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("aaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(path + "*")
	if len(matches) != 0 {
		t.Fatalf("files left behind: %v", matches)
	}
	if _, _, found, _ := m.Get("aaaaaaaaaaaaaaaa"); found {
		t.Fatal("registry row left behind")
	}
	if n, _ := m.Count(); n != 0 {
		t.Fatalf("count = %d", n)
	}
}

func TestResetReseeds(t *testing.T) {
	m, _ := newManager(t, 5)
	st, _, _ := m.Ensure("aaaaaaaaaaaaaaaa", "A")
	seeded := leadCount(t, st)
	st.CreateLead(store.LeadInput{Nome: "Extra", Celular: "+5511977776666", Estagio: "lead", UnitID: firstUnit(t, st), StepID: firstStep(t, st)})
	fresh, err := m.Reset("aaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if leadCount(t, fresh) != seeded {
		t.Fatalf("reset must return to the seeded data, got %d leads", leadCount(t, fresh))
	}
	if _, found, _ := fresh.LeadByPhone("+5511977776666"); found {
		t.Fatal("the extra lead survived the reset")
	}
	if _, err := m.Reset("ffffffffffffffff"); err != ErrNotFound {
		t.Fatalf("resetting an unknown workspace: %v", err)
	}
}
