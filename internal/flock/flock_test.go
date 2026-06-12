package flock

import (
	"path/filepath"
	"testing"
)

func TestAcquire_FirstHolderSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.lock")
	f, ok, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected first acquire to succeed")
	}
	defer f.Close()
}

func TestAcquire_SecondHolderBlocks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.lock")

	f1, ok1, err := Acquire(path)
	if err != nil || !ok1 {
		t.Fatalf("first acquire failed: ok=%v err=%v", ok1, err)
	}
	defer f1.Close()

	f2, ok2, err := Acquire(path)
	if err != nil {
		t.Fatalf("second acquire returned error: %v", err)
	}
	if ok2 {
		t.Fatal("expected second acquire to fail")
	}
	if f2 != nil {
		t.Fatal("expected nil file on failure")
	}
}

func TestAcquire_ReacquireAfterRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.lock")

	f1, _, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f1.Close(); err != nil {
		t.Fatal(err)
	}

	f2, ok, err := Acquire(path)
	if err != nil || !ok {
		t.Fatalf("re-acquire failed: ok=%v err=%v", ok, err)
	}
	defer f2.Close()
}
