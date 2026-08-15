package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	kid := flag.String("kid", "", "key ID used in the JWT kid header")
	outputDirectory := flag.String("out", "", "output directory for private.pem and public/<kid>.pem")
	flag.Parse()

	if strings.TrimSpace(*kid) == "" || strings.ContainsAny(*kid, `/\\\x00`) {
		fatal("--kid is required and must not contain path separators")
	}
	if strings.TrimSpace(*outputDirectory) == "" {
		fatal("--out is required")
	}

	privatePath := filepath.Join(*outputDirectory, "private.pem")
	publicDirectory := filepath.Join(*outputDirectory, "public")
	publicPath := filepath.Join(publicDirectory, *kid+".pem")
	for _, path := range []string{privatePath, publicPath} {
		if _, err := os.Stat(path); err == nil {
			fatal("refusing to overwrite existing file %s", path)
		} else if !os.IsNotExist(err) {
			fatal("inspect %s: %v", path, err)
		}
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatal("generate Ed25519 key: %v", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		fatal("encode private key: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		fatal("encode public key: %v", err)
	}
	if err := os.MkdirAll(publicDirectory, 0o700); err != nil {
		fatal("create output directory: %v", err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0o600); err != nil {
		fatal("write private key: %v", err)
	}
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0o644); err != nil {
		_ = os.Remove(privatePath)
		fatal("write public key: %v", err)
	}
	fmt.Printf("generated kid=%s\nprivate=%s\npublic=%s\n", *kid, privatePath, publicPath)
}

func fatal(format string, arguments ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
