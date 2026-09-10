package codex

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/creds"
)

const (
	authFileName   = "auth.json"
	apiKeyAuthMode = "apikey"
	homeEnv        = "CODEX_HOME"
)

var ErrAPIKeyMode = errors.New("codex: logged in with an API key; plan limits do not apply")

var errNoToken = errors.New("codex: auth.json has no access_token; run `codex login`")

type AuthSource struct {
	Path string
}

func DefaultAuthPath() (string, error) {
	if home := os.Getenv(homeEnv); home != "" {
		return filepath.Join(home, authFileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("codex: locate home dir: %w", err)
	}
	return filepath.Join(home, ".codex", authFileName), nil
}

func (s *AuthSource) Name() string { return "codex-auth" }

func (s *AuthSource) Load() (*creds.Credentials, error) {
	path := s.Path
	if path == "" {
		p, err := DefaultAuthPath()
		if err != nil {
			return nil, err
		}
		path = p
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, creds.ErrNotAvailable
		}
		return nil, fmt.Errorf("codex: read %s: %w", path, err)
	}

	c, err := parseAuthJSON(data)
	if err != nil {
		if errors.Is(err, ErrAPIKeyMode) || errors.Is(err, errNoToken) {
			return nil, err
		}
		return nil, fmt.Errorf("codex: parse %s: %w", path, err)
	}
	c.SourceName = s.Name()
	return c, nil
}

type authFile struct {
	AuthMode     string          `json:"auth_mode"`
	OpenAIAPIKey json.RawMessage `json:"OPENAI_API_KEY"`
	Tokens       *struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

func (af *authFile) hasAPIKey() bool {
	raw := bytes.TrimSpace(af.OpenAIAPIKey)
	return len(raw) > 0 && string(raw) != "null" && string(raw) != `""`
}

func parseAuthJSON(data []byte) (*creds.Credentials, error) {
	var af authFile
	if err := json.Unmarshal(data, &af); err != nil {
		return nil, err
	}
	if af.AuthMode == apiKeyAuthMode {
		return nil, ErrAPIKeyMode
	}
	token, accountID := "", ""
	if af.Tokens != nil {
		token = strings.TrimSpace(af.Tokens.AccessToken)
		accountID = strings.TrimSpace(af.Tokens.AccountID)
	}
	if token == "" {
		if af.hasAPIKey() {
			return nil, ErrAPIKeyMode
		}
		return nil, errNoToken
	}

	c := &creds.Credentials{AccessToken: token, AccountID: accountID}
	claims := decodeClaims(token)
	if claims.Exp > 0 {
		c.ExpiresAt = time.Unix(claims.Exp, 0).UTC()
	}
	if c.AccountID == "" {
		c.AccountID = claims.Auth.AccountID
	}
	return c, nil
}

type tokenClaims struct {
	Exp  int64 `json:"exp"`
	Auth struct {
		AccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

func decodeClaims(token string) tokenClaims {
	var claims tokenClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return claims
	}
	_ = json.Unmarshal(payload, &claims)
	return claims
}
