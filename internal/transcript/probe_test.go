package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeFindsLatestHit(t *testing.T) {
	dir := t.TempDir()
	// Two project subdirs, each with a transcript; the limit hit is in one.
	mustWrite(t, filepath.Join(dir, "proj-a", "session.jsonl"),
		`{"type":"user","timestamp":"2026-06-12T10:00:00Z","message":{"role":"user","content":"hi"}}`+"\n")
	mustWrite(t, filepath.Join(dir, "proj-b", "session.jsonl"),
		`{"type":"assistant","timestamp":"2026-06-12T16:45:00Z","isApiErrorMessage":true,"message":{"role":"assistant","content":[{"type":"text","text":"You've hit your session limit · resets 9:30pm"}]}}`+"\n")

	p := NewProbe(WithProjectsDir(dir), WithLocation(time.UTC))
	now := time.Date(2026, 6, 12, 18, 0, 0, 0, time.UTC)

	hit, err := p.Probe(now)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if hit == nil {
		t.Fatal("expected a limit hit")
	}
	if hit.Message != limitMessage {
		t.Errorf("Message = %q, want %q", hit.Message, limitMessage)
	}
	if hit.ResetsAt == nil {
		t.Fatal("expected ResetsAt set")
	}
	want := time.Date(2026, 6, 12, 21, 30, 0, 0, time.UTC)
	if !hit.ResetsAt.Equal(want) {
		t.Errorf("ResetsAt = %v, want %v", *hit.ResetsAt, want)
	}
	wantDetected := time.Date(2026, 6, 12, 16, 45, 0, 0, time.UTC)
	if !hit.DetectedAt.Equal(wantDetected) {
		t.Errorf("DetectedAt = %v, want %v", hit.DetectedAt, wantDetected)
	}
}

func TestProbeNoHitReturnsNil(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "proj", "session.jsonl"),
		`{"type":"user","timestamp":"2026-06-12T10:00:00Z","message":{"role":"user","content":"all good"}}`+"\n")

	p := NewProbe(WithProjectsDir(dir), WithLocation(time.UTC))
	hit, err := p.Probe(time.Now())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if hit != nil {
		t.Fatalf("expected nil hit, got %+v", hit)
	}
}

func TestProbeMissingDirIsBestEffort(t *testing.T) {
	p := NewProbe(WithProjectsDir(filepath.Join(t.TempDir(), "does-not-exist")), WithLocation(time.UTC))
	hit, err := p.Probe(time.Now())
	if err != nil {
		t.Fatalf("Probe on missing dir should not error, got: %v", err)
	}
	if hit != nil {
		t.Fatalf("expected nil hit, got %+v", hit)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
