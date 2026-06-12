package logfile

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRotate_MissingPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")
	if err := Rotate(path, 10, 3); err != nil {
		t.Fatalf("expected no error on missing path, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file should still not exist")
	}
}

func TestRotate_UnderCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")
	writeFile(t, path, "abc")
	if err := Rotate(path, 10, 3); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "abc" {
		t.Fatalf("path content changed: %q", got)
	}
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal(".1 should not exist under cap")
	}
}

func TestRotate_AtCap_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")
	writeFile(t, path, "0123456789") // 10 bytes
	if err := Rotate(path, 10, 3); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "0123456789" {
		t.Fatalf("expected no rotation at cap, got %q", got)
	}
}

func TestRotate_OverCap_NoExistingBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")
	writeFile(t, path, "0123456789AB") // 12 bytes > 10
	if err := Rotate(path, 10, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("original path should be moved to .1")
	}
	if got := readFile(t, path+".1"); got != "0123456789AB" {
		t.Fatalf(".1 content unexpected: %q", got)
	}
}

func TestRotate_OverCap_ShiftsAndDropsOldest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")

	// Start with: messages.log = "new", .1 = "a", .2 = "b", .3 = "c"
	writeFile(t, path, "00000000000000000000")  // 20 bytes > 10
	writeFile(t, path+".1", "a")
	writeFile(t, path+".2", "b")
	writeFile(t, path+".3", "c")

	if err := Rotate(path, 10, 3); err != nil {
		t.Fatal(err)
	}

	// Expected after rotation:
	//   .3 = previous .2 ("b")  (previous .3 "c" was dropped)
	//   .2 = previous .1 ("a")
	//   .1 = previous path ("0...0")
	//   path = gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("path should be moved away")
	}
	if got := readFile(t, path+".1"); got != "00000000000000000000" {
		t.Fatalf(".1: %q", got)
	}
	if got := readFile(t, path+".2"); got != "a" {
		t.Fatalf(".2: %q", got)
	}
	if got := readFile(t, path+".3"); got != "b" {
		t.Fatalf(".3: %q", got)
	}
	// .4 must not exist (only 3 backups).
	if _, err := os.Stat(path + ".4"); !os.IsNotExist(err) {
		t.Fatal(".4 should not exist")
	}
}

func TestAppendJSONL_CreatesAtMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")

	rec := map[string]any{"msg_id": "abc", "text": "hi"}
	if err := AppendJSONL(path, rec, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm got %o, want 0600", got)
	}
}

func TestAppendJSONL_RetightensMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")
	// Pre-create with loose perms.
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendJSONL(path, map[string]string{"k": "v"}, 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm got %o, want 0600", got)
	}
}

func TestAppendJSONL_AppendsOnePerCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")

	for i := 0; i < 3; i++ {
		rec := map[string]any{"i": i}
		if err := AppendJSONL(path, rec, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	body := readFile(t, path)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), body)
	}
	for i, line := range lines {
		var got map[string]int
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if got["i"] != i {
			t.Fatalf("line %d: got %v", i, got)
		}
	}
}

func TestAppendJSONL_HandlesNonASCII(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "messages.log")
	rec := map[string]string{"text": "日本語 — emoji 👋"}
	if err := AppendJSONL(path, rec, 0o600); err != nil {
		t.Fatal(err)
	}
	// Single line, parseable, round-trips.
	f, _ := os.Open(path)
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan()
	var got map[string]string
	if err := json.Unmarshal(sc.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v (line=%q)", err, sc.Text())
	}
	if got["text"] != rec["text"] {
		t.Fatalf("got %q, want %q", got["text"], rec["text"])
	}
}

// Catch the "%d" format-spec drift the hard way: rotation must use
// consecutive integers, not %s + ad-hoc concat.
func TestRotate_BackupNameFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.log")
	writeFile(t, path, strings.Repeat("a", 100))
	if err := Rotate(path, 10, 5); err != nil {
		t.Fatal(err)
	}
	// Only .1 should exist after first rotation.
	for i := 1; i <= 5; i++ {
		_, err := os.Stat(fmt.Sprintf("%s.%d", path, i))
		exists := err == nil
		wantExists := i == 1
		if exists != wantExists {
			t.Fatalf(".%d exists=%v, want %v", i, exists, wantExists)
		}
	}
}
