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
