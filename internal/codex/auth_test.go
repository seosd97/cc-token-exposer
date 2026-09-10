package codex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
)

func syntheticJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc(payload) + ".synthetic-signature"
}

func authJSON(t *testing.T, v map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	return b
}

func TestParseAuthJSONChatGPTMode(t *testing.T) {
	exp := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	token := syntheticJWT(t, map[string]any{
		"exp": exp.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-claim",
			"chatgpt_plan_type":  "pro",
		},
	})
	c, err := parseAuthJSON(authJSON(t, map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens":         map[string]any{"access_token": token, "refresh_token": "rt", "id_token": "it", "account_id": "acct-from-file"},
		"last_refresh":   "2026-09-08T09:37:46Z",
	}))
	if err != nil {
		t.Fatalf("parseAuthJSON: %v", err)
	}
	if c.AccessToken != token {
		t.Fatalf("access token not carried through")
	}
	if c.AccountID != "acct-from-file" {
		t.Fatalf("AccountID = %q, want the file's account_id to win over the claim", c.AccountID)
	}
	if !c.ExpiresAt.Equal(exp) {
		t.Fatalf("ExpiresAt = %v, want JWT exp %v", c.ExpiresAt, exp)
	}
}

func TestParseAuthJSONAccountIDFallsBackToTheJWTClaim(t *testing.T) {
	token := syntheticJWT(t, map[string]any{
		"exp":                         time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-from-claim"},
	})
	c, err := parseAuthJSON(authJSON(t, map[string]any{
		"auth_mode": "chatgpt",
		"tokens":    map[string]any{"access_token": token},
	}))
	if err != nil {
		t.Fatalf("parseAuthJSON: %v", err)
	}
	if c.AccountID != "acct-from-claim" {
		t.Fatalf("AccountID = %q, want the claim fallback", c.AccountID)
	}
}

func TestParseAuthJSONOpaqueTokenHasNoExpiry(t *testing.T) {
	c, err := parseAuthJSON(authJSON(t, map[string]any{
		"tokens": map[string]any{"access_token": "not-a-jwt", "account_id": "acct"},
	}))
	if err != nil {
		t.Fatalf("parseAuthJSON: %v", err)
	}
	if !c.ExpiresAt.IsZero() || c.AccountID != "acct" || c.AccessToken != "not-a-jwt" {
		t.Fatalf("opaque token should load with unknown expiry: %+v", c)
	}
	if c.Expired(time.Now()) {
		t.Fatal("unknown expiry must not count as expired")
	}
}

func TestParseAuthJSONAPIKeyModes(t *testing.T) {
	cases := []struct {
		name string
		auth map[string]any
		want error
	}{
		{"auth_mode apikey wins even with tokens", map[string]any{"auth_mode": "apikey", "OPENAI_API_KEY": "sk-x", "tokens": map[string]any{"access_token": "tok"}}, ErrAPIKeyMode},
		{"api key without tokens", map[string]any{"OPENAI_API_KEY": "sk-x"}, ErrAPIKeyMode},
		{"nothing at all", map[string]any{"OPENAI_API_KEY": nil, "tokens": nil}, errNoToken},
		{"empty token string", map[string]any{"tokens": map[string]any{"access_token": "  "}}, errNoToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAuthJSON(authJSON(t, tc.auth))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseAuthJSONMalformedDoesNotEchoContent(t *testing.T) {
	_, err := parseAuthJSON([]byte(`{"tokens":{"access_token":"SECRET-TOKEN-VALUE"`))
	if err == nil {
		t.Fatal("truncated JSON must fail")
	}
	if strings.Contains(err.Error(), "SECRET-TOKEN-VALUE") {
		t.Fatalf("parse error leaked file content: %v", err)
	}
}

func TestAuthSourceLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, authJSON(t, map[string]any{
		"auth_mode": "chatgpt",
		"tokens":    map[string]any{"access_token": "tok", "account_id": "acct"},
	}), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	c, err := (&AuthSource{Path: path}).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.SourceName != "codex-auth" || c.AccessToken != "tok" || c.AccountID != "acct" {
		t.Fatalf("unexpected credentials: %s account=%q", c, c.AccountID)
	}

	if _, err := (&AuthSource{Path: filepath.Join(dir, "missing.json")}).Load(); !errors.Is(err, creds.ErrNotAvailable) {
		t.Fatalf("missing file err = %v, want ErrNotAvailable", err)
	}

	if err := os.WriteFile(path, authJSON(t, map[string]any{"auth_mode": "apikey"}), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := (&AuthSource{Path: path}).Load(); !errors.Is(err, ErrAPIKeyMode) {
		t.Fatalf("apikey err = %v, want ErrAPIKeyMode unwrapped", err)
	}
}

func TestAuthSourceResolvesThroughTheCredsResolver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, authJSON(t, map[string]any{"tokens": map[string]any{"access_token": "tok", "account_id": "acct"}}), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c, err := creds.NewResolver(&AuthSource{Path: path}).Resolve()
	if err != nil || c.AccountID != "acct" {
		t.Fatalf("Resolve = %v, %v; want account acct", c, err)
	}
}

func TestDefaultAuthPathHonorsCodexHome(t *testing.T) {
	t.Setenv(homeEnv, "/tmp/codex-home")
	p, err := DefaultAuthPath()
	if err != nil {
		t.Fatalf("DefaultAuthPath: %v", err)
	}
	if p != filepath.Join("/tmp/codex-home", "auth.json") {
		t.Fatalf("path = %s, want CODEX_HOME/auth.json", p)
	}

	t.Setenv(homeEnv, "")
	p, err = DefaultAuthPath()
	if err != nil {
		t.Fatalf("DefaultAuthPath: %v", err)
	}
	if filepath.Base(filepath.Dir(p)) != ".codex" || filepath.Base(p) != "auth.json" {
		t.Fatalf("path = %s, want ~/.codex/auth.json", p)
	}
}
