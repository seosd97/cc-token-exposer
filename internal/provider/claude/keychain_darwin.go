//go:build darwin

package claude

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/seosd97/cc-token-exposer/internal/provider"
)

const keychainService = "Claude Code-credentials"

const errSecItemNotFound = 44

type KeychainSource struct {
	Service string
	run     func(service string) ([]byte, error)
}

func newKeychainSource() provider.Source { return &KeychainSource{} }

func (s *KeychainSource) Name() string { return "keychain" }

func (s *KeychainSource) Load() (*provider.Credentials, error) {
	service := s.Service
	if service == "" {
		service = keychainService
	}

	run := s.run
	if run == nil {
		run = runSecurity
	}

	out, err := run(service)
	if err != nil {
		if isItemNotFound(err) {
			return nil, provider.ErrNotAvailable
		}
		return nil, fmt.Errorf("claude: keychain lookup: %w", err)
	}

	c, perr := parseCredentialsJSON(out)
	if perr != nil {
		return nil, fmt.Errorf("claude: parse keychain item: %w", perr)
	}
	c.SourceName = s.Name()
	return c, nil
}

func runSecurity(service string) ([]byte, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", service, "-w")
	return cmd.Output()
}

func isItemNotFound(err error) bool {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode() == errSecItemNotFound
	}
	return false
}
