//go:build !darwin

package creds

type KeychainSource struct{}

func newKeychainSource() Source { return &KeychainSource{} }

func (s *KeychainSource) Name() string { return "keychain" }

func (s *KeychainSource) Load() (*Credentials, error) { return nil, ErrNotAvailable }
