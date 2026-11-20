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
	subject := flag.String("sub", "dev-1", "identificador estável do desenvolvedor")
	name := flag.String("name", "Dev Exemplo", "nome de exibição do desenvolvedor")
	ttl := flag.Duration("ttl", time.Minute, "por quanto tempo o token continua válido")
	base := flag.String("url", "", "imprime a URL de login completa a partir desta base, por exemplo http://localhost:8080")
	envFile := flag.String("env-file", ".env", "arquivo opcional com linhas CHAVE=valor")
	flag.Parse()

	if err := config.LoadDotEnv(*envFile, func(k, v string) { _ = os.Setenv(k, v) }, os.LookupEnv); err != nil {
		log.Fatalf("lendo %s: %v", *envFile, err)
	}
	secret := strings.TrimSpace(os.Getenv("PORTAL_SSO_SECRET"))
	if len(secret) < config.MinPortalSecretLen {
		log.Fatal("defina PORTAL_SSO_SECRET (pelo menos 16 caracteres) no ambiente ou no arquivo de variáveis")
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
