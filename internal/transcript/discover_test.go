package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFindTranscriptsTopKNewestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "p", "s"+string(rune('a'+i))+".jsonl")
		mustWrite(t, p, "x\n")
		if err := os.Chtimes(p, base.Add(time.Duration(i)*time.Minute), base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Chtimes %s: %v", p, err)
		}
	}
	mustWrite(t, filepath.Join(dir, "p", "notes.md"), "nope")

	files, err := FindTranscripts(dir, 3)
	if err != nil {
		t.Fatalf("FindTranscripts: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("got %d files, want 3", len(files))
	}
	if !files[0].ModTime.Equal(base.Add(4 * time.Minute)) {
		t.Errorf("files[0] mtime = %v, want the newest", files[0].ModTime)
	}
	if !files[2].ModTime.Equal(base.Add(2 * time.Minute)) {
		t.Errorf("files[2] mtime = %v, want the third newest (k-th)", files[2].ModTime)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.Path] {
			t.Fatalf("duplicate path %s", f.Path)
		}
		seen[f.Path] = true
		if !f.ModTime.After(files[0].ModTime.Add(-time.Hour)) {
			t.Errorf("unexpected mtime %v", f.ModTime)
		}
	}
}

func TestFindTranscriptsUnbounded(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		mustWrite(t, filepath.Join(dir, "p", "s"+string(rune('a'+i))+".jsonl"), "x\n")
	}
	files, err := FindTranscripts(dir, 0)
	if err != nil {
		t.Fatalf("FindTranscripts: %v", err)
	}
	if len(files) != 5 {
		t.Fatalf("k<=0 must collect everything, got %d", len(files))
	}
	for i := 1; i < len(files); i++ {
		if files[i].ModTime.After(files[i-1].ModTime) {
			t.Fatalf("files not sorted newest-first at %d", i)
		}
	}
}

func TestFindTranscriptsMissingDir(t *testing.T) {
	files, err := FindTranscripts(filepath.Join(t.TempDir(), "nope"), 10)
	if err != nil {
		t.Fatalf("missing dir should not error, got: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %d", len(files))
	}
}
