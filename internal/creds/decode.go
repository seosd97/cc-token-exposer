package creds

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type credentialsFile struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
		ExpiresAt   int64  `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

var errNoToken = errors.New("creds: credentials blob has no accessToken")

func parseCredentialsJSON(data []byte) (*Credentials, error) {
	var cf credentialsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(cf.ClaudeAiOauth.AccessToken)
	if token == "" {
		return nil, errNoToken
	}
	c := &Credentials{AccessToken: token}
	if cf.ClaudeAiOauth.ExpiresAt > 0 {
		c.ExpiresAt = time.UnixMilli(cf.ClaudeAiOauth.ExpiresAt)
	}
	return c, nil
}
