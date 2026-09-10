package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fatal("usage: CCX_SIGNING_KEY=<base64 ed25519 private key> ccx-sign <checksums.txt>")
	}
	keyB64 := os.Getenv("CCX_SIGNING_KEY")
	if keyB64 == "" {
		fatal("ccx-sign: CCX_SIGNING_KEY is required")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		fatalf("ccx-sign: decode key: %v", err)
	}
	priv := ed25519.PrivateKey(keyBytes)
	if len(priv) != ed25519.PrivateKeySize {
		fatal("ccx-sign: key has wrong length")
	}

	f, err := os.Open(os.Args[1])
	if err != nil {
		fatalf("ccx-sign: %v", err)
	}
	data, err := io.ReadAll(f)
	_ = f.Close()
	if err != nil {
		fatalf("ccx-sign: %v", err)
	}

	sig := ed25519.Sign(priv, data)
	out := os.Args[1] + ".sig"
	if err := os.WriteFile(out, []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		fatalf("ccx-sign: %v", err)
	}
	fmt.Printf("signed %s (pubkey %s)\n", out, base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
