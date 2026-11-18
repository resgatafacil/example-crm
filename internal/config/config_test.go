package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func validKey() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
}

func names(problems []Problem) []string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Name)
	}
	return out
}

func TestMissingVariablesAreListedExactly(t *testing.T) {
	_, problems := Load(env(nil))
	got := strings.Join(names(problems), ",")
	want := "RESGATA_CLIENT_ID,RESGATA_WEBHOOK_SECRET,APP_ENCRYPTION_KEY"
	if got != want {
		t.Fatalf("problems = %s, want %s", got, want)
	}
}

func TestValidConfigAndDefaults(t *testing.T) {
	cfg, problems := Load(env(map[string]string{
		"RESGATA_CLIENT_ID": "id", "RESGATA_WEBHOOK_SECRET": "x", "APP_ENCRYPTION_KEY": validKey(),
	}))
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if !cfg.Sandbox() || cfg.PublicURL != "http://localhost:8080" || cfg.RateLimit != 60 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.WebhookURL() != "http://localhost:8080/webhooks/resgata" || cfg.RedirectURI() != "http://localhost:8080/resgata/callback" {
		t.Fatalf("unexpected urls: %s %s", cfg.WebhookURL(), cfg.RedirectURI())
	}
}

func TestInvalidValues(t *testing.T) {
	_, problems := Load(env(map[string]string{
		"RESGATA_CLIENT_ID": "id", "RESGATA_WEBHOOK_SECRET": "x", "APP_ENCRYPTION_KEY": "bad",
		"RESGATA_ENVIRONMENT": "prod", "CRM_API_RATE_LIMIT": "0",
	}))
	got := strings.Join(names(problems), ",")
	want := "CRM_API_RATE_LIMIT,RESGATA_ENVIRONMENT,APP_ENCRYPTION_KEY"
	if got != want {
		t.Fatalf("problems = %s, want %s", got, want)
	}
}

func TestDotEnvDoesNotOverrideExistingValues(t *testing.T) {
	path := t.TempDir() + "/.env"
	writeFile(t, path, "# comment\nA=1\nB=\"two words\"\nC='3'\nEXISTING=file\n")
	set := map[string]string{"EXISTING": "process"}
	err := LoadDotEnv(path,
		func(k, v string) { set[k] = v },
		func(k string) (string, bool) { v, ok := set[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	if set["A"] != "1" || set["B"] != "two words" || set["C"] != "3" || set["EXISTING"] != "process" {
		t.Fatalf("unexpected env: %v", set)
	}
}

func TestDotEnvMissingFileIsFine(t *testing.T) {
	if err := LoadDotEnv("/nonexistent/.env", func(string, string) {}, func(string) (string, bool) { return "", false }); err != nil {
		t.Fatal(err)
	}
}

func baseEnv() map[string]string {
	return map[string]string{"RESGATA_CLIENT_ID": "id", "RESGATA_WEBHOOK_SECRET": "x", "APP_ENCRYPTION_KEY": validKey()}
}

func TestPortalDefaultsAreOffByDefault(t *testing.T) {
	cfg, problems := Load(env(baseEnv()))
	if len(problems) != 0 || cfg.PortalMode() {
		t.Fatalf("portal mode must be off without PORTAL_SSO_SECRET: %v %+v", problems, cfg)
	}
	if cfg.DataDir != "data" || cfg.PortalURL != "https://developers.resgatafacil.com" {
		t.Fatalf("unexpected defaults: %s %s", cfg.DataDir, cfg.PortalURL)
	}
	if cfg.WorkspaceTTL.Hours() != 24 || cfg.SessionTTL.Hours() != 8 || cfg.MaxWorkspaces != 50 || cfg.PurgeInterval.Minutes() != 15 {
		t.Fatalf("unexpected limits: %+v", cfg)
	}
}

func TestPortalSettingsAreRead(t *testing.T) {
	values := baseEnv()
	values["PORTAL_SSO_SECRET"] = strings.Repeat("s", 32)
	values["PORTAL_URL"] = "https://portal.example.com/"
	values["WORKSPACE_TTL_HOURS"] = "6"
	values["SESSION_TTL_HOURS"] = "2"
	values["MAX_WORKSPACES"] = "7"
	values["WORKSPACE_PURGE_INTERVAL_MINUTES"] = "5"
	values["DATA_DIR"] = "/srv/funil"
	values["PUBLIC_URL"] = "https://demo.example.com"
	cfg, problems := Load(env(values))
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if !cfg.PortalMode() || cfg.PortalURL != "https://portal.example.com" || cfg.DataDir != "/srv/funil" || !cfg.SecureCookies() {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.WorkspaceTTL.Hours() != 6 || cfg.SessionTTL.Hours() != 2 || cfg.MaxWorkspaces != 7 || cfg.PurgeInterval.Minutes() != 5 {
		t.Fatalf("unexpected limits: %+v", cfg)
	}
}

func TestDataDirFollowsTheDatabasePath(t *testing.T) {
	values := baseEnv()
	values["DATABASE_PATH"] = "/data/funil.db"
	cfg, _ := Load(env(values))
	if cfg.DataDir != "/data" {
		t.Fatalf("data dir = %s", cfg.DataDir)
	}
}

func TestInvalidPortalSettings(t *testing.T) {
	values := baseEnv()
	values["PORTAL_SSO_SECRET"] = "short"
	values["WORKSPACE_TTL_HOURS"] = "0"
	values["SESSION_TTL_HOURS"] = "abc"
	values["MAX_WORKSPACES"] = "-1"
	values["WORKSPACE_PURGE_INTERVAL_MINUTES"] = "0"
	_, problems := Load(env(values))
	got := strings.Join(names(problems), ",")
	want := "PORTAL_SSO_SECRET,WORKSPACE_TTL_HOURS,SESSION_TTL_HOURS,MAX_WORKSPACES,WORKSPACE_PURGE_INTERVAL_MINUTES"
	if got != want {
		t.Fatalf("problems = %s, want %s", got, want)
	}
}
