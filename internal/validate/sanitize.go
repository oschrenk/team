package validate

import (
	"regexp"
	"strings"
	"unicode"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// SanitizeForStdout makes s safe to print as a Claude Code monitor
// notification: ANSI escapes stripped, newlines/CRs collapsed to ↵,
// other category-C runes dropped, TAB preserved.
//
// Mirrors shared.py::sanitize_for_stdout.
func SanitizeForStdout(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteRune(r)
		case r == '\n', r == '\r':
			b.WriteRune('↵')
		case unicode.Is(unicode.C, r):
			// drop
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TruncateForStdout returns the prefix of s up to maxRunes codepoints,
// along with a wasTruncated flag and the original codepoint count.
//
// Mirrors shared.py::truncate_for_stdout. Note: the cap is a codepoint
// count, not a byte count.
func TruncateForStdout(s string, maxRunes int) (truncated string, wasTruncated bool, fullLen int) {
	runes := []rune(s)
	fullLen = len(runes)
	if fullLen <= maxRunes {
		return s, false, fullLen
	}
	return string(runes[:maxRunes]), true, fullLen
}
