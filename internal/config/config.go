package config

import (
	"bufio"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
)

const (
	EnvSandbox = "sandbox"
	EnvLive    = "live"
)

type Problem struct {
	Name string
	Hint string
}

type Config struct {
	Addr          string
	PublicURL     string
	DatabasePath  string
	EncryptionKey []byte
	AdminPassword string
	RateLimit     int
	APIURL        string
	AppURL        string
	ClientID      string
	AppKey        string
	WebhookSecret string
	Environment   string
}

func (c Config) Sandbox() bool {
	return c.Environment == EnvSandbox
}

func (c Config) WebhookURL() string {
	return c.PublicURL + "/webhooks/resgata"
}

func (c Config) RedirectURI() string {
	return c.PublicURL + "/resgata/callback"
}

func Load(getenv func(string) string) (Config, []Problem) {
	var problems []Problem
	value := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}

	cfg := Config{
		Addr:          value("APP_ADDR", ":8080"),
		DatabasePath:  value("DATABASE_PATH", "data/funil.db"),
		AdminPassword: getenv("ADMIN_PASSWORD"),
		APIURL:        strings.TrimRight(value("RESGATA_API_URL", "https://app.resgatafacil.com/api/platform/v1"), "/"),
		AppURL:        strings.TrimRight(value("RESGATA_APP_URL", "https://app.resgatafacil.com"), "/"),
		ClientID:      value("RESGATA_CLIENT_ID", ""),
		AppKey:        value("RESGATA_APP_KEY", ""),
		WebhookSecret: value("RESGATA_WEBHOOK_SECRET", ""),
		Environment:   value("RESGATA_ENVIRONMENT", EnvSandbox),
		RateLimit:     60,
	}
	cfg.PublicURL = strings.TrimRight(value("PUBLIC_URL", defaultPublicURL(cfg.Addr)), "/")

	if raw := value("CRM_API_RATE_LIMIT", ""); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			problems = append(problems, Problem{"CRM_API_RATE_LIMIT", "Número inteiro maior que zero (requisições por minuto por chave)."})
		} else {
			cfg.RateLimit = n
		}
	}

	if cfg.Environment != EnvSandbox && cfg.Environment != EnvLive {
		problems = append(problems, Problem{"RESGATA_ENVIRONMENT", "Use sandbox ou live."})
	}

	if cfg.ClientID == "" {
		problems = append(problems, Problem{"RESGATA_CLIENT_ID", "Identificador do aplicativo (app_…), mostrado no portal de desenvolvedores."})
	}

	if cfg.WebhookSecret == "" {
		problems = append(problems, Problem{"RESGATA_WEBHOOK_SECRET", "Segredo de assinatura do endpoint de webhook (whsec_…), mostrado no portal."})
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(getenv("APP_ENCRYPTION_KEY")))
	if err != nil || len(key) != 32 {
		problems = append(problems, Problem{"APP_ENCRYPTION_KEY", "32 bytes aleatórios em base64. Gere com: openssl rand -base64 32"})
	} else {
		cfg.EncryptionKey = key
	}

	return cfg, problems
}

func defaultPublicURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

func LoadDotEnv(path string, setenv func(string, string), lookup func(string) (string, bool)) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, raw, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if _, exists := lookup(name); exists {
			continue
		}
		setenv(name, unquote(strings.TrimSpace(raw)))
	}
	return scanner.Err()
}

func unquote(raw string) string {
	if len(raw) >= 2 && (raw[0] == '"' && raw[len(raw)-1] == '"' || raw[0] == '\'' && raw[len(raw)-1] == '\'') {
		return raw[1 : len(raw)-1]
	}
	return raw
}
