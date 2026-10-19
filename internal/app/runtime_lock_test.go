package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeLockRejectsConcurrentProcessAndRecovers(t *testing.T) {
	dir := t.TempDir()
	first, err := lockRuntime(dir)
	if err != nil {
		t.Fatal(err)
	}
	if competing, err := lockRuntime(dir); err == nil {
		competing.Close()
		t.Fatal("concurrent runtime accepted")
	}
	first.Close()
	replacement, err := lockRuntime(dir)
	if err != nil {
		t.Fatal(err)
	}
	replacement.Close()
}

func TestRuntimeLockRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "runtime.lock")); err != nil {
		t.Fatal(err)
	}
	if lock, err := lockRuntime(dir); err == nil {
		lock.Close()
		t.Fatal("symlink lock accepted")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "preserve" {
		t.Fatal("unrelated target changed")
	}
}
