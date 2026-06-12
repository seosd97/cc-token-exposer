package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("transcript: resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// FindTranscripts returns all *.jsonl files under projectsDir, newest first.
func FindTranscripts(projectsDir string) ([]string, error) {
	type entry struct {
		path    string
		modTime int64
	}
	var entries []entry

	err := filepath.WalkDir(projectsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		entries = append(entries, entry{path: path, modTime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("transcript: walk %s: %w", projectsDir, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime > entries[j].modTime
	})
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.path
	}
	return paths, nil
}
