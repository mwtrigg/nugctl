package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shrinkRenameRetry lowers the retry loop to test speed and restores the
// production defaults afterward.
func shrinkRenameRetry(t *testing.T) {
	t.Helper()
	origAttempts, origDelay := renameAttempts, renameRetryDelay
	renameAttempts = 2
	renameRetryDelay = time.Millisecond
	t.Cleanup(func() {
		renameAttempts, renameRetryDelay = origAttempts, origDelay
	})
}

func TestReplaceViaShuffle_Success(t *testing.T) {
	shrinkRenameRetry(t)
	dir := t.TempDir()
	self := filepath.Join(dir, "nugctl.exe")
	tmp := self + ".new"

	if err := os.WriteFile(self, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("seeding self: %v", err)
	}
	if err := os.WriteFile(tmp, []byte("new-binary"), 0o755); err != nil {
		t.Fatalf("seeding tmp: %v", err)
	}

	if err := replaceViaShuffle(self, tmp); err != nil {
		t.Fatalf("replaceViaShuffle: %v", err)
	}

	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("reading self after swap: %v", err)
	}
	if string(got) != "new-binary" {
		t.Errorf("self content = %q, want new-binary", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp should have been renamed away, stat err = %v", err)
	}
	if _, err := os.Stat(self + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old should have been cleaned up, stat err = %v", err)
	}
}

func TestReplaceViaShuffle_RestoresSelfOnFailure(t *testing.T) {
	shrinkRenameRetry(t)
	dir := t.TempDir()
	self := filepath.Join(dir, "nugctl.exe")
	tmp := self + ".new" // deliberately never created, so installing it fails

	if err := os.WriteFile(self, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("seeding self: %v", err)
	}

	if err := replaceViaShuffle(self, tmp); err == nil {
		t.Fatal("expected an error when tmp doesn't exist")
	}

	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("self should have been restored, but is missing: %v", err)
	}
	if string(got) != "old-binary" {
		t.Errorf("restored self content = %q, want old-binary", got)
	}
	if _, err := os.Stat(self + ".old"); !os.IsNotExist(err) {
		t.Errorf("stray .old left behind after restore, stat err = %v", err)
	}
}
