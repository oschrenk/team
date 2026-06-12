// Package validate enforces the wire-protocol's name/label rules and
// sanitizes incoming text before it lands in a Claude Code monitor
// notification.
package validate

import (
	"path/filepath"
	"regexp"
	"strings"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Name reports whether s is a valid addressable agent name.
// Strict ASCII, [a-z0-9][a-z0-9-]{0,39}, case-sensitive.
func Name(s string) bool {
	return nameRE.MatchString(s)
}

// AutoNameFromCwd derives a Name-valid handle from the basename of cwd.
// Returns "" if no valid name can be derived.
//
// Mirrors shared.py::auto_name_from_cwd: lower-case, collapse runs of
// non-alphanumeric to a single '-', strip leading/trailing '-', cap at
// 40 chars. The trailing cap may leave a result that fails Name() —
// in that case return "".
func AutoNameFromCwd(cwd string) string {
	base := strings.ToLower(filepath.Base(cwd))
	var b strings.Builder
	b.Grow(len(base))
	for _, ch := range base {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		default:
			s := b.String()
			if len(s) > 0 && s[len(s)-1] != '-' {
				b.WriteRune('-')
			}
		}
	}
	name := strings.Trim(b.String(), "-")
	if len(name) > 40 {
		name = name[:40]
	}
	if !Name(name) {
		return ""
	}
	return name
}
