package claude

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/seosd97/cc-token-exposer/internal/provider"
)

type credentialsFile struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

var errNoToken = errors.New("claude: credentials blob has no accessToken")

func parseCredentialsJSON(data []byte) (*provider.Credentials, error) {
	var cf credentialsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(cf.ClaudeAiOauth.AccessToken)
	if token == "" {
		return nil, errNoToken
	}
	c := &provider.Credentials{AccessToken: token}
	if cf.ClaudeAiOauth.ExpiresAt > 0 {
		c.ExpiresAt = time.UnixMilli(cf.ClaudeAiOauth.ExpiresAt)
	}
	return c, nil
}

func DefaultCredentials() *provider.Resolver {
	return provider.NewResolver(
		&FileSource{},
		newKeychainSource(),
	)
}
