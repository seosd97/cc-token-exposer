package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Apply atomically replaces the executable at target with newBinary by writing
// a sibling temp file and renaming it over the target (the running process
// keeps its own open inode on Unix).
func Apply(newBinary []byte, target string) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".ccx-update-*")
	if err != nil {
		return fmt.Errorf("selfupdate: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(newBinary); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("selfupdate: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("selfupdate: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("selfupdate: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("selfupdate: replace %s: %w", target, err)
	}
	return nil
}

// IsHomebrew reports whether execPath resolves into a Homebrew Cellar, meaning
// the install is brew-managed and should be updated with `brew upgrade`.
func IsHomebrew(execPath string) bool {
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		resolved = execPath
	}
	return strings.Contains(resolved, "/Cellar/")
}
