package transcript

import (
	"container/heap"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func DefaultProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("transcript: resolve home: %w", err)
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

type TranscriptFile struct {
	Path    string
	ModTime time.Time
}

// minModHeap keeps the newest k files seen during a traversal, evicting the
// oldest on overflow; a bounded heap avoids collecting and sorting every
// transcript in the tree (which can be large on long-lived machines).
type minModHeap []TranscriptFile

func (h minModHeap) Len() int           { return len(h) }
func (h minModHeap) Less(i, j int) bool { return h[i].ModTime.Before(h[j].ModTime) }
func (h minModHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minModHeap) Push(x any)        { *h = append(*h, x.(TranscriptFile)) }
func (h *minModHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// FindTranscripts returns the k most recently modified *.jsonl files under
// projectsDir, newest first. k <= 0 collects everything.
func FindTranscripts(projectsDir string, k int) ([]TranscriptFile, error) {
	var h minModHeap

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
		f := TranscriptFile{Path: path, ModTime: info.ModTime()}
		if k > 0 && h.Len() == k {
			if !f.ModTime.After(h[0].ModTime) {
				return nil
			}
			heap.Pop(&h)
		}
		heap.Push(&h, f)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("transcript: walk %s: %w", projectsDir, err)
	}

	files := make([]TranscriptFile, 0, h.Len())
	for h.Len() > 0 {
		files = append(files, heap.Pop(&h).(TranscriptFile))
	}
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	return files, nil
}
