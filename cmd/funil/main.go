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
)

func main() {
	noSeed := flag.Bool("no-seed", false, "do not create demo data when the database is empty")
	envFile := flag.String("env-file", ".env", "optional file with KEY=value lines")
	flag.Parse()

	if err := config.LoadDotEnv(*envFile, func(k, v string) { _ = os.Setenv(k, v) }, os.LookupEnv); err != nil {
		log.Fatalf("reading %s: %v", *envFile, err)
	}

	cfg, problems := config.Load(os.Getenv)
	if len(problems) > 0 {
		serveSetup(cfg, problems)
		return
	}

	st, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("opening the database: %v", err)
	}
	defer st.Close()

	if empty, err := st.Empty(); err == nil && empty && !*noSeed {
		if err := st.Seed(); err != nil {
			log.Fatalf("seeding demo data: %v", err)
		}
		log.Printf("created demo data")
	}

	box, err := crypto.NewBox(cfg.EncryptionKey)
	if err != nil {
		log.Fatal(err)
	}
	server, err := web.New(cfg, st, box, resgata.NewClient(cfg.APIURL, nil))
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Funil Demo on %s (Resgata environment: %s)", cfg.PublicURL, cfg.Environment)
	log.Printf("webhook endpoint: %s", cfg.WebhookURL())
	if cfg.AdminPassword == "" {
		log.Printf("ADMIN_PASSWORD is not set: the admin UI is open to anyone who can reach this address")
	}
	run(cfg.Addr, server.Handler())
}

func serveSetup(cfg config.Config, problems []config.Problem) {
	log.Printf("configuration incomplete, serving the setup screen on %s", cfg.Addr)
	for _, p := range problems {
		log.Printf("  missing or invalid: %s", p.Name)
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
