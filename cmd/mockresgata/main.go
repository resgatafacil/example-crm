package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/config"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/mockresgata"
)

func main() {
	envFile := flag.String("env-file", ".env", "optional file with KEY=value lines")
	flag.Parse()
	_ = config.LoadDotEnv(*envFile, func(k, v string) { _ = os.Setenv(k, v) }, os.LookupEnv)

	addr := getenv("MOCK_ADDR", ":9090")
	secret := os.Getenv("RESGATA_WEBHOOK_SECRET")
	if secret == "" {
		raw := make([]byte, 16)
		_, _ = rand.Read(raw)
		secret = "mock_" + hex.EncodeToString(raw)
		log.Printf("RESGATA_WEBHOOK_SECRET is not set, using a random one for this run: %s", secret)
	}

	mock := mockresgata.New(mockresgata.Options{
		ClientID:      getenv("RESGATA_CLIENT_ID", "app_mock"),
		WebhookURL:    getenv("MOCK_WEBHOOK_URL", "http://localhost:8080/webhooks/resgata"),
		WebhookSecret: secret,
	})

	log.Printf("mock Resgata Fácil platform on %s, sending webhooks to %s", addr, getenv("MOCK_WEBHOOK_URL", "http://localhost:8080/webhooks/resgata"))
	srv := &http.Server{Addr: addr, Handler: mock.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func getenv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
