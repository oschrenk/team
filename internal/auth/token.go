// Package auth covers the bearer-token + server-identity verification
// that gates every WebSocket / inspect connection.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	tokenBytes       = 32
	readAttempts     = 50
	readAttemptDelay = 5 * time.Millisecond
)

// EnsureToken returns the bearer token at path. Creates one (32 bytes,
// base64-RawURL) on first call. Concurrent callers race O_CREAT|O_EXCL;
// losers retry-read the winner's body.
//
// Refuses symlinks and non-regular files at path — defense against
// "swap the token file for a symlink to /etc/something" attacks.
func EnsureToken(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		return writeNewToken(f)
	}
	if !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	return readExistingToken(path)
}

func writeNewToken(f *os.File) (string, error) {
	defer f.Close()
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := f.Write([]byte(tok)); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return tok, nil
}

func readExistingToken(path string) (string, error) {
	li, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if li.Mode()&fs.ModeSymlink != 0 || !li.Mode().IsRegular() {
		return "", fmt.Errorf("token path %s is not a regular file", path)
	}
	// Re-tighten perms in case the file was created with looser perms.
	_ = os.Chmod(path, 0o600)

	for i := 0; i < readAttempts; i++ {
		data, err := os.ReadFile(path)
		if err == nil {
			tok := strings.TrimSpace(string(data))
			if tok != "" {
				return tok, nil
			}
		}
		time.Sleep(readAttemptDelay)
	}
	return "", fmt.Errorf("token file at %s is empty after %d retries", path, readAttempts)
}
