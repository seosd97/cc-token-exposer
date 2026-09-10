//go:build !darwin

package claude

import "github.com/seosd97/cc-token-exposer/internal/provider"

type KeychainSource struct{}

func newKeychainSource() provider.Source { return &KeychainSource{} }

func (s *KeychainSource) Name() string { return "keychain" }

func (s *KeychainSource) Load() (*provider.Credentials, error) { return nil, provider.ErrNotAvailable }
