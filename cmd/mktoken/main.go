package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"git.resgatafacil.com/resgatafacil/example-crm/internal/config"
	"git.resgatafacil.com/resgatafacil/example-crm/internal/portal"
)

func main() {
	subject := flag.String("sub", "dev-1", "stable id of the developer")
	name := flag.String("name", "Dev Exemplo", "display name of the developer")
	ttl := flag.Duration("ttl", time.Minute, "how long the token stays valid")
	base := flag.String("url", "", "print a full login URL under this base, for example http://localhost:8080")
	envFile := flag.String("env-file", ".env", "optional file with KEY=value lines")
	flag.Parse()

	if err := config.LoadDotEnv(*envFile, func(k, v string) { _ = os.Setenv(k, v) }, os.LookupEnv); err != nil {
		log.Fatalf("reading %s: %v", *envFile, err)
	}
	secret := strings.TrimSpace(os.Getenv("PORTAL_SSO_SECRET"))
	if len(secret) < config.MinPortalSecretLen {
		log.Fatal("set PORTAL_SSO_SECRET (at least 16 characters) in the environment or in the env file")
	}

	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		log.Fatal(err)
	}
	now := time.Now()
	token, err := portal.Sign(secret, portal.Claims{
		Subject: *subject, Name: *name, IssuedAt: now.Unix(), ExpiresAt: now.Add(*ttl).Unix(), ID: hex.EncodeToString(raw),
	})
	if err != nil {
		log.Fatal(err)
	}
	if *base == "" {
		fmt.Println(token)
		return
	}
	fmt.Printf("%s/portal-login?token=%s\n", strings.TrimRight(*base, "/"), url.QueryEscape(token))
}
