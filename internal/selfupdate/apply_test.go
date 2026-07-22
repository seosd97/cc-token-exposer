package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyReplacesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ccx")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	newBin := []byte("new binary bytes")
	if err := Apply(newBin, target); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != string(newBin) {
		t.Errorf("target = %q, want %q", got, newBin)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("target not executable: %v", info.Mode())
	}

	// No leftover temp files in the directory.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("directory has stray files: %v", entries)
	}
}

func TestApplyMissingDir(t *testing.T) {
	if err := Apply([]byte("x"), filepath.Join(t.TempDir(), "nope", "ccx")); err == nil {
		t.Fatal("expected error when target directory does not exist")
	}
}

func TestIsHomebrew(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/opt/homebrew/Cellar/ccx/0.1.0/bin/ccx", true},
		{"/usr/local/Cellar/ccx/0.1.0/bin/ccx", true},
		{"/home/linuxbrew/.linuxbrew/Cellar/ccx/0.1.0/bin/ccx", true},
		{"/Users/me/go/bin/ccx", false},
		{"/usr/local/bin/ccx", false},
	}
	for _, tc := range cases {
		if got := IsHomebrew(tc.path); got != tc.want {
			t.Errorf("IsHomebrew(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
