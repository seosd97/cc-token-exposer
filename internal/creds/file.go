package creds

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const defaultFileName = ".credentials.json"

type FileSource struct {
	Path string
}

func DefaultFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("creds: locate home dir: %w", err)
	}
	return filepath.Join(home, ".claude", defaultFileName), nil
}

func (s *FileSource) Name() string { return "file" }

func (s *FileSource) Load() (*Credentials, error) {
	path := s.Path
	if path == "" {
		p, err := DefaultFilePath()
		if err != nil {
			return nil, err
		}
		path = p
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotAvailable
		}
		return nil, fmt.Errorf("creds: read %s: %w", path, err)
	}

	c, err := parseCredentialsJSON(data)
	if err != nil {
		return nil, fmt.Errorf("creds: parse %s: %w", path, err)
	}
	c.SourceName = s.Name()
	return c, nil
}
