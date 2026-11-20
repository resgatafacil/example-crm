package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/config"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/crypto"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/resgata"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/store"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/web"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/workspace"
)

func main() {
	noSeed := flag.Bool("no-seed", false, "não cria os dados de demonstração quando o banco está vazio")
	envFile := flag.String("env-file", ".env", "arquivo opcional com linhas CHAVE=valor")
	flag.Parse()

	if err := config.LoadDotEnv(*envFile, func(k, v string) { _ = os.Setenv(k, v) }, os.LookupEnv); err != nil {
		log.Fatalf("lendo %s: %v", *envFile, err)
	}

	cfg, problems := config.Load(os.Getenv)
	if len(problems) > 0 {
		serveSetup(cfg, problems)
		return
	}

	backend, closeBackend := openBackend(cfg, *noSeed)
	defer closeBackend()

	box, err := crypto.NewBox(cfg.EncryptionKey)
	if err != nil {
		log.Fatal(err)
	}
	server, err := web.New(cfg, backend, box, resgata.NewClient(cfg.APIURL, nil))
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Funil Demo on %s (Resgata environment: %s)", cfg.PublicURL, cfg.Environment)
	log.Printf("webhook endpoint: %s", cfg.WebhookURL())
	switch {
	case cfg.PortalMode():
		log.Printf("acesso pelo portal ativado: entre por %s (workspaces em %s, ociosos após %s, no máximo %d)", cfg.PortalURL, cfg.DataDir, cfg.WorkspaceTTL, cfg.MaxWorkspaces)
		if cfg.AdminPassword != "" {
			log.Printf("ADMIN_PASSWORD é ignorado enquanto PORTAL_SSO_SECRET estiver definido")
		}
	case cfg.AdminPassword == "":
		log.Printf("ADMIN_PASSWORD não está definido: a interface administrativa está aberta para quem alcançar este endereço")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunPurger(ctx, cfg.PurgeInterval)
	run(cfg.Addr, server.Handler())
}

func openBackend(cfg config.Config, noSeed bool) (web.Backend, func()) {
	if cfg.PortalMode() {
		manager, err := workspace.NewManager(cfg.DataDir, cfg.MaxWorkspaces)
		if err != nil {
			log.Fatalf("abrindo os workspaces: %v", err)
		}
		return web.Backend{Workspaces: manager}, manager.Close
	}

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("abrindo o banco de dados: %v", err)
	}
	if empty, err := st.Empty(); err == nil && empty && !noSeed {
		if err := st.Seed(); err != nil {
			log.Fatalf("seeding demo data: %v", err)
		}
		log.Printf("created demo data")
	}
	return web.Backend{Local: st}, func() { _ = st.Close() }
}

func serveSetup(cfg config.Config, problems []config.Problem) {
	log.Printf("configuração incompleta, exibindo a tela de configuração em %s", cfg.Addr)
	for _, p := range problems {
		log.Printf("  ausente ou inválida: %s", p.Name)
	}
	suggestion := ""
	for _, p := range problems {
		if p.Name == "APP_ENCRYPTION_KEY" {
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err == nil {
				suggestion = base64.StdEncoding.EncodeToString(raw)
			}
		}
	}
	run(cfg.Addr, web.SetupHandler(problems, suggestion))
}

func run(addr string, handler http.Handler) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-stop.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = srv.Shutdown(shutdown)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
